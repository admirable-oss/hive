package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

// EnvCommand is hive's hidden command that prints its environment.
const EnvCommand = "__env"

// PrintEnv writes the process's environment as one line of JSON, for
// Toolchains.Prepare, and returns the exit status.
func PrintEnv(w io.Writer) int {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
			env[k] = v
		}
	}
	data, err := json.Marshal(env)
	if err != nil {
		return 1
	}
	if _, err := fmt.Fprintf(w, "\n%s\n", data); err != nil {
		return 1
	}
	return 0
}

// toolchainTimeout bounds making a toolchain's environment: the first
// nix develop of a flake can build for minutes.
const toolchainTimeout = 15 * time.Minute

// markers are the files that say which toolchain a directory uses, in the
// order auto prefers them. Each kind's files are also what its cached
// environment is keyed on.
var markers = []struct {
	kind  Toolchain
	files []string // the first that exists marks the directory
	keyed []string // more files the environment depends on
}{
	{ToolchainNix, []string{"flake.nix"}, []string{"flake.lock"}},
	{ToolchainDevcontainer, []string{".devcontainer/devcontainer.json", ".devcontainer.json"}, nil},
	{ToolchainMise, []string{"mise.toml", ".mise.toml", ".tool-versions"}, []string{"mise.local.toml", ".mise.local.toml", "mise.lock"}},
	{ToolchainDirenv, []string{".envrc"}, nil},
}

// programs are what each toolchain needs on the daemon's PATH.
var programs = map[Toolchain]string{
	ToolchainNix: "nix", ToolchainDevcontainer: "devcontainer", ToolchainMise: "mise", ToolchainDirenv: "direnv",
}

// Resolved is the toolchain an environment starts its panes in.
type Resolved struct {
	Kind Toolchain `json:"kind"`
	// Dir is where its marker is: the environment's directory or one
	// above it, inside the project.
	Dir string `json:"dir,omitempty"`
	// Cached says its environment is ready, so panes open at once.
	Cached bool `json:"cached,omitempty"`
	// Problem says why it cannot be used.
	Problem string `json:"problem,omitempty"`
	key     string
}

// Prepared is what starting a pane in a toolchain takes: variables to
// add, and for a devcontainer the command its programs run through.
type Prepared struct {
	Env  map[string]string
	Wrap []string
}

// ToolchainConfig configures Toolchains.
type ToolchainConfig struct {
	// Dir caches toolchains' environments.
	Dir string
	// EnvCommand prints its own environment as a JSON object (hive
	// __env); toolchains run it to learn theirs.
	EnvCommand []string
	Run        Runner
	// LookPath finds programs (default exec.LookPath).
	LookPath func(string) (string, error)
	// Environ is the daemon's own environment, which a toolchain's is
	// compared with (default os.Environ).
	Environ func() []string
}

// Toolchains finds projects' toolchains and makes their environments,
// cached by the files that define them.
type Toolchains struct {
	cfg ToolchainConfig
	mu  sync.Mutex // one toolchain made at a time
}

// NewToolchains builds the toolchains.
func NewToolchains(cfg ToolchainConfig) *Toolchains {
	if cfg.Run == nil {
		cfg.Run = ExecRunner
	}
	if cfg.LookPath == nil {
		cfg.LookPath = exec.LookPath
	}
	if cfg.Environ == nil {
		cfg.Environ = os.Environ
	}
	return &Toolchains{cfg: cfg}
}

// Resolve finds the toolchain of kind (auto: whichever is there) for an
// environment at dir, looking up to the project's root.
func (t *Toolchains) Resolve(kind Toolchain, dir, root string) Resolved {
	if kind == "" || kind == ToolchainNone {
		return Resolved{Kind: ToolchainNone}
	}
	for d := dir; ; d = filepath.Dir(d) {
		for _, m := range markers {
			if kind != ToolchainAuto && m.kind != kind {
				continue
			}
			if !slices.ContainsFunc(m.files, func(f string) bool { return isFile(filepath.Join(d, filepath.FromSlash(f))) }) {
				continue
			}
			r := Resolved{Kind: m.kind, Dir: d}
			if _, err := t.cfg.LookPath(programs[m.kind]); err != nil {
				r.Problem = fmt.Sprintf("toolchain %s: %s is not installed (on the daemon's PATH)", m.kind, programs[m.kind])
				return r
			}
			if m.kind != ToolchainDevcontainer {
				r.key = toolchainKey(m.kind, d, append(slices.Clone(m.files), m.keyed...))
				_, r.Cached = t.cached(r.key)
			}
			return r
		}
		if samePath(d, root) || filepath.Dir(d) == d {
			break
		}
	}
	if kind == ToolchainAuto {
		return Resolved{Kind: ToolchainNone}
	}
	return Resolved{Kind: kind, Problem: fmt.Sprintf("toolchain %s: no %s in %s or above it", kind, markerNames(kind), dir)}
}

// Prepare makes the environment r names, from the cache when its files
// did not change.
func (t *Toolchains) Prepare(ctx context.Context, r Resolved) (Prepared, error) {
	switch {
	case r.Problem != "":
		return Prepared{}, errors.New(r.Problem)
	case r.Kind == ToolchainNone || r.Kind == "":
		return Prepared{}, nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, toolchainTimeout)
	defer cancel()
	if r.Kind == ToolchainDevcontainer {
		// devcontainer up starts the container, or finds it running.
		if _, err := t.cfg.Run(ctx, r.Dir, []string{"devcontainer", "up", "--workspace-folder", r.Dir}, nil); err != nil {
			return Prepared{}, fmt.Errorf("toolchain devcontainer: %w", err)
		}
		return Prepared{Wrap: []string{"devcontainer", "exec", "--workspace-folder", r.Dir}}, nil
	}
	if env, ok := t.cached(r.key); ok {
		return Prepared{Env: env}, nil
	}
	if len(t.cfg.EnvCommand) == 0 {
		return Prepared{}, errors.New("toolchains need hive's own program, which was not found")
	}
	var prefix []string
	switch r.Kind {
	case ToolchainNix:
		prefix = []string{"nix", "develop", r.Dir, "--command"}
	case ToolchainMise:
		prefix = []string{"mise", "exec", "--"}
	case ToolchainDirenv:
		prefix = []string{"direnv", "exec", r.Dir}
	}
	out, err := t.cfg.Run(ctx, r.Dir, append(prefix, t.cfg.EnvCommand...), nil)
	if err != nil {
		return Prepared{}, fmt.Errorf("toolchain %s: %w", r.Kind, err)
	}
	var inside map[string]string
	if err := json.Unmarshal(lastLine(out), &inside); err != nil {
		return Prepared{}, fmt.Errorf("toolchain %s: reading its environment: %w", r.Kind, err)
	}
	env := envChanges(t.cfg.Environ(), inside)
	if err := t.store(r.key, r.Kind, env); err != nil {
		return Prepared{}, err
	}
	return Prepared{Env: env}, nil
}

// lastLine is the JSON a toolchain's run printed last: shells and hooks
// may print before it.
func lastLine(out []byte) []byte {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return []byte(lines[len(lines)-1])
}

// ignoredVars change from one shell to the next, or belong to the pane.
var ignoredVars = []string{"PWD", "OLDPWD", "SHLVL", "_", "TERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "COLUMNS", "LINES", "SHELL"}

// envChanges are the variables a toolchain sets or changes.
func envChanges(base []string, inside map[string]string) map[string]string {
	before := map[string]string{}
	for _, kv := range base {
		if k, v, ok := strings.Cut(kv, "="); ok {
			before[k] = v
		}
	}
	out := map[string]string{}
	for k, v := range inside {
		switch {
		case slices.Contains(ignoredVars, k), strings.HasPrefix(k, "HIVE_"), strings.HasPrefix(k, "DIRENV_"),
			strings.HasPrefix(k, "__MISE_"), strings.HasPrefix(k, "MISE_SHELL"):
			continue
		case before[k] == v:
			continue
		}
		out[k] = v
	}
	return out
}

type cacheFile struct {
	Schema int               `json:"schema"`
	Kind   Toolchain         `json:"kind"`
	Made   time.Time         `json:"made"`
	Env    map[string]string `json:"env"`
}

func (t *Toolchains) cachePath(key string) string {
	return filepath.Join(t.cfg.Dir, key+".json")
}

func (t *Toolchains) cached(key string) (map[string]string, bool) {
	if key == "" || t.cfg.Dir == "" {
		return nil, false
	}
	var f cacheFile
	if err := jsonfile.Read(t.cachePath(key), &f); err != nil || f.Schema != schema {
		return nil, false
	}
	return f.Env, true
}

func (t *Toolchains) store(key string, kind Toolchain, env map[string]string) error {
	if key == "" || t.cfg.Dir == "" {
		return nil
	}
	if err := os.MkdirAll(t.cfg.Dir, 0o700); err != nil {
		return err
	}
	return jsonfile.Write(t.cachePath(key), cacheFile{Schema: schema, Kind: kind, Made: time.Now().UTC(), Env: env})
}

// toolchainKey names a toolchain's environment by what it is made from:
// the kind, the directory and its files' contents.
func toolchainKey(kind Toolchain, dir string, files []string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00", kind, dir)
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			fmt.Fprintf(h, "%s\x00-\x00", f)
		case err != nil:
			fmt.Fprintf(h, "%s\x00?%v\x00", f, err)
		default:
			fmt.Fprintf(h, "%s\x00%d\x00", f, len(data))
			h.Write(data)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func markerNames(kind Toolchain) string {
	for _, m := range markers {
		if m.kind == kind {
			return strings.Join(m.files, " or ")
		}
	}
	return string(kind)
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}
