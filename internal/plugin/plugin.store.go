package plugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// Errors of the store.
var (
	ErrNotFound = errors.New("no such plugin")
	ErrExists   = errors.New("plugin already installed")
	ErrDeclined = errors.New("not installed: the plugin was not trusted")
)

// Installed is a plugin the user installed or linked.
type Installed struct {
	ID string `json:"id"`
	// Source is where it came from: a git URL or path, or the linked
	// directory. Commit is the installed commit (installs from git).
	Source      string    `json:"source"`
	Ref         string    `json:"ref,omitempty"`
	Commit      string    `json:"commit,omitempty"`
	Linked      bool      `json:"linked,omitempty"`
	Enabled     bool      `json:"enabled"`
	InstalledAt time.Time `json:"installed_at"`
}

// Plugin is an installed plugin with its manifest, as found on disk.
type Plugin struct {
	Installed
	Dir      string    `json:"dir"`
	Manifest *Manifest `json:"manifest,omitempty"`
	// Problem says why it cannot run (its manifest is broken, it needs a
	// newer hive); empty when it can.
	Problem string `json:"problem,omitempty"`
}

// Store keeps plugins under one directory (<HIVE_HOME>/plugins): each in
// <id>/ (a symlink when linked), the list in registry.json, and their own
// data in .data/<id>/ (plugin IDs never start with a dot).
type Store struct {
	dir string
	mu  sync.Mutex
}

// NewStore returns the store in dir.
func NewStore(dir string) *Store { return &Store{dir: dir} }

// Dir is where plugin id lives.
func (s *Store) Dir(id string) string { return filepath.Join(s.dir, id) }

// DataDir is plugin id's own writable directory.
func (s *Store) DataDir(id string) string { return filepath.Join(s.dir, ".data", id) }

func (s *Store) registry() string { return filepath.Join(s.dir, "registry.json") }

type registryFile struct {
	Schema  int         `json:"schema"`
	Plugins []Installed `json:"plugins"`
}

func (s *Store) loadLocked() ([]Installed, error) {
	var f registryFile
	err := jsonfile.Read(s.registry(), &f)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return f.Plugins, err
}

func (s *Store) saveLocked(list []Installed) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	slices.SortFunc(list, func(a, b Installed) int { return strings.Compare(a.ID, b.ID) })
	return jsonfile.Write(s.registry(), registryFile{Schema: 1, Plugins: list})
}

// List returns every installed plugin with its manifest.
func (s *Store) List(hiveVersion string) ([]Plugin, error) {
	s.mu.Lock()
	list, err := s.loadLocked()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	out := make([]Plugin, 0, len(list))
	for _, in := range list {
		p := Plugin{Installed: in, Dir: s.Dir(in.ID)}
		m, err := LoadManifest(p.Dir)
		switch {
		case err != nil:
			p.Problem = err.Error()
		case m.ID != in.ID:
			p.Problem = fmt.Sprintf("its manifest now says id %q", m.ID)
		default:
			p.Manifest = m
			if err := m.Supported(hiveVersion); err != nil {
				p.Problem = err.Error()
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// Get returns one installed plugin.
func (s *Store) Get(id, hiveVersion string) (Plugin, error) {
	list, err := s.List(hiveVersion)
	if err != nil {
		return Plugin{}, err
	}
	for _, p := range list {
		if p.ID == id {
			return p, nil
		}
	}
	return Plugin{}, fmt.Errorf("%w %q (see `hive plugin list`)", ErrNotFound, id)
}

// Fetched is a plugin cloned for installing, not yet installed.
type Fetched struct {
	Manifest *Manifest
	Source   string
	Ref      string
	Commit   string
	dir      string // the clone
}

// Fetch clones a plugin from git (anything git clone accepts: a URL or a
// path to a repository) and reads its manifest, for the user to review
// with Preview before Install. Discard it if not installing.
func (s *Store) Fetch(ctx context.Context, source, ref string) (*Fetched, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(s.dir, ".fetch-")
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(tmp, "src")
	args := []string{"clone", "--quiet", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, "--", source, dir)
	if err := git(ctx, "", args...); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, fmt.Errorf("fetch %s: %w", source, err)
	}
	f := &Fetched{Source: source, Ref: ref, dir: dir}
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD")
	cmd.Stdout = &out
	if cmd.Run() == nil {
		f.Commit = strings.TrimSpace(out.String())
	}
	if f.Manifest, err = LoadManifest(dir); err != nil {
		_ = os.RemoveAll(tmp)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s has no %s at its top", source, ManifestFile)
		}
		return nil, err
	}
	return f, nil
}

// Preview describes the fetched plugin for the user deciding to trust it.
func (f *Fetched) Preview() string {
	src := f.Source
	if f.Commit != "" {
		src += " @ " + f.Commit[:min(len(f.Commit), 12)]
	}
	return f.Manifest.Preview(src)
}

// Discard removes the clone.
func (f *Fetched) Discard() { _ = os.RemoveAll(filepath.Dir(f.dir)) }

// Install puts a fetched plugin in place, enabled.
func (s *Store) Install(f *Fetched, hiveVersion string) (Installed, error) {
	defer f.Discard()
	if err := f.Manifest.Supported(hiveVersion); err != nil {
		return Installed{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadLocked()
	if err != nil {
		return Installed{}, err
	}
	id := f.Manifest.ID
	if slices.ContainsFunc(list, func(in Installed) bool { return in.ID == id }) {
		return Installed{}, fmt.Errorf("%w: %s (uninstall it first)", ErrExists, id)
	}
	if err := os.Rename(f.dir, s.Dir(id)); err != nil {
		return Installed{}, err
	}
	in := Installed{ID: id, Source: f.Source, Ref: f.Ref, Commit: f.Commit, Enabled: true, InstalledAt: time.Now()}
	return in, s.saveLocked(append(list, in))
}

// Link installs the plugin in dir as it is, by a symlink: changes there
// apply at the next reload (for developing a plugin).
func (s *Store) Link(dir, hiveVersion string) (Installed, *Manifest, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Installed{}, nil, err
	}
	m, err := LoadManifest(abs)
	if err != nil {
		return Installed{}, nil, err
	}
	if err := m.Supported(hiveVersion); err != nil {
		return Installed{}, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadLocked()
	if err != nil {
		return Installed{}, nil, err
	}
	if slices.ContainsFunc(list, func(in Installed) bool { return in.ID == m.ID }) {
		return Installed{}, nil, fmt.Errorf("%w: %s (uninstall it first)", ErrExists, m.ID)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return Installed{}, nil, err
	}
	if err := os.Symlink(abs, s.Dir(m.ID)); err != nil {
		return Installed{}, nil, err
	}
	in := Installed{ID: m.ID, Source: abs, Linked: true, Enabled: true, InstalledAt: time.Now()}
	return in, m, s.saveLocked(append(list, in))
}

// SetEnabled turns a plugin on or off.
func (s *Store) SetEnabled(id string, on bool) (Installed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadLocked()
	if err != nil {
		return Installed{}, err
	}
	i := slices.IndexFunc(list, func(in Installed) bool { return in.ID == id })
	if i < 0 {
		return Installed{}, fmt.Errorf("%w %q", ErrNotFound, id)
	}
	list[i].Enabled = on
	return list[i], s.saveLocked(list)
}

// Uninstall removes a plugin: its files (only the link, when linked) and
// its data.
func (s *Store) Uninstall(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadLocked()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(list, func(in Installed) bool { return in.ID == id })
	if i < 0 {
		return fmt.Errorf("%w %q", ErrNotFound, id)
	}
	if err := os.RemoveAll(s.Dir(id)); err != nil { // a symlink is removed, not followed
		return err
	}
	if err := os.RemoveAll(s.DataDir(id)); err != nil {
		return err
	}
	return s.saveLocked(slices.Delete(list, i, i+1))
}

func git(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// Never wait for a password prompt nobody sees.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}
