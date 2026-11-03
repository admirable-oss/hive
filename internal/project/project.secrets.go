package project

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// A secret reference names a value in the person's own store, never the
// value itself:
//
//	keychain:<service>[/<account>]           the macOS keychain (security)
//	secret-service:<attr>=<value>[,…]        the Secret Service: GNOME Keyring, KWallet (secret-tool)
//	pass:<path>                              pass, the standard Unix password manager
//	op://<vault>/<item>/<field>              the 1Password CLI (op read)
var secretSchemes = []string{"keychain:", "secret-service:", "pass:", "op://"}

// secretTimeout bounds reading one secret: a store may ask the person to
// unlock it.
const secretTimeout = 2 * time.Minute

// CheckSecretRef checks a secret reference's form.
func CheckSecretRef(ref string) error {
	scheme, rest, ok := cutScheme(ref)
	switch {
	case !ok:
		return fmt.Errorf("%q: a reference to your secret store: %s", ref, strings.Join(secretSchemes, ", "))
	case strings.TrimSpace(rest) == "" || strings.ContainsAny(rest, "\n\r\x00"):
		return fmt.Errorf("%q: names nothing", ref)
	case scheme == "secret-service:":
		for _, kv := range strings.Split(rest, ",") {
			if k, v, ok := strings.Cut(kv, "="); !ok || k == "" || v == "" {
				return fmt.Errorf("%q: attributes as attr=value, separated by commas", ref)
			}
		}
	case scheme == "op://" && strings.Count(rest, "/") < 2:
		return fmt.Errorf("%q: op://<vault>/<item>/<field>", ref)
	case scheme == "keychain:" && strings.HasPrefix(rest, "/"):
		return fmt.Errorf("%q: keychain:<service>[/<account>]", ref)
	}
	return nil
}

func cutScheme(ref string) (scheme, rest string, ok bool) {
	for _, s := range secretSchemes {
		if r, found := strings.CutPrefix(ref, s); found {
			return s, r, true
		}
	}
	return "", "", false
}

// SecretStore reads secrets by reference.
type SecretStore interface {
	Resolve(ctx context.Context, ref string) (string, error)
}

// Runner runs a program in dir and returns what it printed.
type Runner func(ctx context.Context, dir string, argv []string, env []string) ([]byte, error)

// ExecRunner runs programs as they are.
func ExecRunner(ctx context.Context, dir string, argv []string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = dir, env
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		if msg != "" {
			return out, fmt.Errorf("%s: %w: %s", argv[0], err, msg)
		}
		return out, fmt.Errorf("%s: %w", argv[0], err)
	}
	return out, nil
}

// ToolStores reads secrets with each store's own command line tool.
type ToolStores struct {
	Run Runner
	// LookPath finds the tools (default exec.LookPath).
	LookPath func(string) (string, error)
}

// Resolve reads the value ref names.
func (t ToolStores) Resolve(ctx context.Context, ref string) (string, error) {
	if err := CheckSecretRef(ref); err != nil {
		return "", err
	}
	scheme, rest, _ := cutScheme(ref)
	var argv []string
	switch scheme {
	case "keychain:":
		service, account, _ := strings.Cut(rest, "/")
		argv = []string{"security", "find-generic-password", "-s", service, "-w"}
		if account != "" {
			argv = append(argv, "-a", account)
		}
	case "secret-service:":
		argv = []string{"secret-tool", "lookup"}
		for _, kv := range strings.Split(rest, ",") {
			k, v, _ := strings.Cut(kv, "=")
			argv = append(argv, k, v)
		}
	case "pass:":
		argv = []string{"pass", "show", rest}
	case "op://":
		argv = []string{"op", "read", "--no-newline", ref}
	}
	look := t.LookPath
	if look == nil {
		look = exec.LookPath
	}
	if _, err := look(argv[0]); err != nil {
		return "", fmt.Errorf("%s needs %s, which is not installed", schemeName(scheme), argv[0])
	}
	ctx, cancel := context.WithTimeout(ctx, secretTimeout)
	defer cancel()
	run := t.Run
	if run == nil {
		run = ExecRunner
	}
	out, err := run(ctx, "", argv, nil)
	if err != nil {
		// The tools' messages name the item, never the value.
		return "", fmt.Errorf("%s: %w", schemeName(scheme), err)
	}
	value := string(out)
	if scheme == "pass:" {
		// pass keeps the password on the first line, notes after it.
		value, _, _ = strings.Cut(value, "\n")
	}
	value = strings.TrimRight(value, "\r\n")
	if value == "" {
		return "", errors.New(schemeName(scheme) + ": the secret is empty")
	}
	return value, nil
}

func schemeName(scheme string) string {
	switch scheme {
	case "keychain:":
		return "the keychain"
	case "secret-service:":
		return "the Secret Service"
	case "pass:":
		return "pass"
	}
	return "1Password"
}
