// Package session names Hive's namespaces. Each session is a separate
// storage root with its own daemon, socket, lock, environments and agents,
// so work in one session cannot affect another. The default session lives
// directly in the base directory (~/.hive); named sessions live in
// base/sessions/<name>.
package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
)

// Default is the session used when none is named.
const Default = "default"

// EnvVar selects the session for commands that do not pass --session. The
// daemon sets it in every pane, so `hive` run by an agent talks to the
// agent's own session.
const EnvVar = "HIVE_SESSION"

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ErrInvalidName is returned for names that are not safe as a directory.
var ErrInvalidName = errors.New("invalid session name (letters, digits, '.', '_' and '-', up to 64 characters)")

// Validate checks a session name.
func Validate(name string) error {
	if name == "" || name == Default || namePattern.MatchString(name) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrInvalidName, name)
}

// Normalize maps "" to Default.
func Normalize(name string) string {
	if name == "" {
		return Default
	}
	return name
}

// Root returns the storage root of session name under base.
func Root(base, name string) (string, error) {
	if err := Validate(name); err != nil {
		return "", err
	}
	if Normalize(name) == Default {
		return base, nil
	}
	return filepath.Join(base, "sessions", name), nil
}

// Info describes a session on disk.
type Info struct {
	Name string `json:"name"`
	Root string `json:"root"`
}

// List returns the default session and every named session that has a
// directory, default first, then by name.
func List(base string) ([]Info, error) {
	out := []Info{{Name: Default, Root: base}}
	entries, err := os.ReadDir(filepath.Join(base, "sessions"))
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && namePattern.MatchString(e.Name()) && e.Name() != Default {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	for _, n := range names {
		out = append(out, Info{Name: n, Root: filepath.Join(base, "sessions", n)})
	}
	return out, nil
}
