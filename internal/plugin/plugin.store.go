package plugin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/admirable-oss/hive/internal/platform"
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
	Source           string `json:"source"`
	Ref              string `json:"ref,omitempty"`
	Commit           string `json:"commit,omitempty"`
	Version          string `json:"version,omitempty"`
	Checksum         string `json:"checksum,omitempty"`
	Registry         bool   `json:"registry,omitempty"`
	PreviousVersion  string `json:"previous_version,omitempty"`
	PreviousRef      string `json:"previous_ref,omitempty"`
	PreviousCommit   string `json:"previous_commit,omitempty"`
	PreviousChecksum string `json:"previous_checksum,omitempty"`
	Linked           bool   `json:"linked,omitempty"`
	Enabled          bool   `json:"enabled"`
	// Granted are the capabilities the user approved: the required ones
	// when installing or updating, optional ones with `hive plugin grant`.
	Granted     []string  `json:"granted,omitempty"`
	InstalledAt time.Time `json:"installed_at"`
	// Trust is what the registry said of the installed release (registry
	// installs): its tier, publisher, signer, provenance and revocation.
	// PreviousTrust is the previous release's, for a rollback.
	Trust
	PreviousTrust *Trust `json:"previous_trust,omitempty"`
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
func NewStore(dir string) *Store {
	abs, err := filepath.Abs(dir)
	if err == nil {
		dir = abs
	}
	return &Store{dir: filepath.Clean(dir)}
}

// lock serializes store mutations across both threads and CLI processes.
func (s *Store) lock() (func(), error) {
	s.mu.Lock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(s.dir, ".plugins.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if err := platform.Lock(file); err != nil {
		_ = file.Close()
		s.mu.Unlock()
		return nil, err
	}
	return func() { _ = platform.Unlock(file); _ = file.Close(); s.mu.Unlock() }, nil
}

// Recover repairs an interrupted registry update/rollback before callers inspect the store.
// Transactions commit when the new installed record reached registry.json; otherwise the
// previous active directory is restored. Plugin data lives separately and is never touched.
func (s *Store) Recover() error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	return s.recoverLocked()
}

func (s *Store) recoverLocked() error {
	path := filepath.Join(s.dir, ".transaction.json")
	var tx storeTransaction
	if err := jsonfile.Read(path, &tx); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read interrupted plugin transaction: %w", err)
	}
	if tx.Schema != 1 || !ValidID(tx.PluginID) {
		return errors.New("invalid interrupted plugin transaction; manual repair required")
	}
	if tx.Next.ID != tx.PluginID || !tx.Next.Registry || tx.Next.Version == "" || len(tx.Next.Commit) != 40 || !isHex(tx.Next.Commit) {
		return errors.New("invalid interrupted plugin release metadata; manual repair required")
	}
	versions := s.VersionsDir(tx.PluginID)
	switch tx.Operation {
	case "update":
		if tx.Next.PreviousVersion == "" || len(tx.Next.PreviousCommit) != 40 ||
			tx.Backup != filepath.Join(versions, safeReleaseDir(tx.Next.PreviousVersion, tx.Next.PreviousCommit)) ||
			filepath.Base(tx.Stage) != "src" || !strings.HasPrefix(filepath.Base(filepath.Dir(tx.Stage)), ".fetch-") {
			return errors.New("invalid interrupted update paths; manual repair required")
		}
	case "rollback":
		if tx.Next.PreviousVersion == "" || len(tx.Next.PreviousCommit) != 40 ||
			tx.Backup != filepath.Join(versions, safeReleaseDir(tx.Next.Version, tx.Next.Commit)) ||
			tx.Stage != filepath.Join(versions, safeReleaseDir(tx.Next.PreviousVersion, tx.Next.PreviousCommit)) {
			return errors.New("invalid interrupted rollback paths; manual repair required")
		}
	default:
		return errors.New("unknown interrupted plugin transaction; manual repair required")
	}
	for _, candidate := range []string{tx.Backup, tx.Stage} {
		if candidate == "" {
			continue
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(s.dir, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("interrupted plugin transaction references a path outside the plugin store; manual repair required")
		}
	}
	list, err := s.loadLocked()
	if err != nil {
		return err
	}
	committed := slices.ContainsFunc(list, func(in Installed) bool {
		return in.ID == tx.PluginID && in.Commit == tx.Next.Commit && in.Version == tx.Next.Version
	})
	active, backup := s.Dir(tx.PluginID), tx.Backup
	if committed {
		if !tx.KeepStage {
			if err := os.RemoveAll(tx.Stage); err != nil {
				return err
			}
		}
	} else {
		if tx.Operation == "rollback" {
			if _, err := os.Lstat(tx.Stage); err == nil {
				if _, activeErr := os.Lstat(active); activeErr == nil {
					if err := os.Rename(active, backup); err != nil {
						return err
					}
				}
				if err := os.Rename(tx.Stage, active); err != nil {
					return fmt.Errorf("restore previous plugin release: %w", err)
				}
			} else if _, backupErr := os.Lstat(backup); backupErr == nil {
				if _, activeErr := os.Lstat(active); errors.Is(activeErr, fs.ErrNotExist) {
					if err := os.Rename(backup, active); err != nil {
						return err
					}
				} else if activeErr != nil {
					return activeErr
				}
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		} else if _, err := os.Lstat(backup); err == nil {
			if err := os.RemoveAll(active); err != nil {
				return err
			}
			if err := os.Rename(backup, active); err != nil {
				return fmt.Errorf("restore previous plugin release: %w", err)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		_ = os.RemoveAll(tx.Stage)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

type storeTransaction struct {
	Schema    int       `json:"schema"`
	PluginID  string    `json:"plugin_id"`
	Operation string    `json:"operation"`
	Backup    string    `json:"backup"`
	Stage     string    `json:"stage"`
	KeepStage bool      `json:"keep_stage,omitempty"`
	Next      Installed `json:"next"`
}

func (s *Store) VersionsDir(id string) string { return filepath.Join(s.dir, ".versions", id) }

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
	if err != nil {
		return nil, fmt.Errorf("installed plugins %s: %w", s.registry(), err)
	}
	return f.Plugins, jsonfile.CheckSchema(s.registry(), f.Schema, 1)
}

func (s *Store) saveLocked(list []Installed) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	slices.SortFunc(list, func(a, b Installed) int { return strings.Compare(a.ID, b.ID) })
	return jsonfile.Write(s.registry(), registryFile{Schema: 1, Plugins: list})
}

// Records returns the installed plugins' records, without their manifests.
func (s *Store) Records() ([]Installed, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.loadLocked()
}

// trust is the record's trust as clients see it: plugins installed from
// source are unlisted.
func (in Installed) trust() Trust {
	if !in.Registry {
		return Trust{Tier: TierUnlisted}
	}
	return in.Trust
}

// List returns every installed plugin with its manifest.
func (s *Store) List(ctx context.Context, hiveVersion string) ([]Plugin, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := s.recoverLocked(); err != nil {
		return nil, err
	}
	list, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	out := make([]Plugin, 0, len(list))
	for _, in := range list {
		p := Plugin{Installed: in, Dir: s.Dir(in.ID)}
		p.Trust = in.trust()
		m, err := LoadManifest(p.Dir)
		switch {
		case err != nil:
			p.Problem = err.Error()
		case m.ID != in.ID:
			p.Problem = fmt.Sprintf("its manifest now says id %q", m.ID)
		default:
			p.Manifest = m
			if err := m.Supported(ctx, hiveVersion); err != nil {
				p.Problem = err.Error()
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// Get returns one installed plugin.
func (s *Store) Get(ctx context.Context, id, hiveVersion string) (Plugin, error) {
	list, err := s.List(ctx, hiveVersion)
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
	Version  string
	Checksum string
	Registry bool
	// Trust is the registry's word on the release (registry fetches).
	Trust            Trust
	checksumVerified bool
	Subdir           string
	dir              string // the clone
	repoDir          string // repository root (may differ from the installed package)
	tmpDir           string // clone parent removed after install or discard
}

const maxPluginArchiveBytes = 64 << 20

// VerifyArchiveChecksum compares a published SHA-256 digest against the
// deterministic tar stream produced by git archive for the fetched commit.
func (f *Fetched) VerifyArchiveChecksum(ctx context.Context, want string) error {
	want = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(want)), "sha256:")
	if len(want) != sha256.Size*2 || !isHex(want) {
		return fmt.Errorf("invalid plugin archive checksum %q", want)
	}
	h := sha256.New()
	w := &boundedHashWriter{h: h, limit: maxPluginArchiveBytes}
	repoDir := f.repoDir
	if repoDir == "" {
		repoDir = f.dir
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "archive", "--format=tar", f.Commit)
	cmd.Stdout = w
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if w.tooLarge {
			return fmt.Errorf("plugin source archive exceeds %d MiB", maxPluginArchiveBytes>>20)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("calculate plugin source checksum: %s: %w", msg, err)
		}
		return fmt.Errorf("calculate plugin source checksum: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("plugin source checksum mismatch: expected sha256:%s, got sha256:%s", want, got)
	}
	f.checksumVerified = true
	return nil
}

type boundedHashWriter struct {
	h        io.Writer
	written  int64
	limit    int64
	tooLarge bool
}

func (w *boundedHashWriter) Write(p []byte) (int, error) {
	if w.written+int64(len(p)) > w.limit {
		w.tooLarge = true
		return 0, fmt.Errorf("archive exceeds size limit")
	}
	n, err := w.h.Write(p)
	w.written += int64(n)
	return n, err
}

// Fetch clones a plugin from git (anything git clone accepts: a URL or a
// path to a repository) and reads its manifest, for the user to review
// with Preview before Install. Discard it if not installing.
func (s *Store) Fetch(ctx context.Context, source, ref string) (*Fetched, error) {
	return s.FetchSubdir(ctx, source, ref, "")
}

// FetchSubdir clones a repository and installs the package rooted at subdir.
// An empty subdir selects the repository root. Registry checksums continue to
// cover the immutable full-repository archive.
func (s *Store) FetchSubdir(ctx context.Context, source, ref, subdir string) (*Fetched, error) {
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
	packageDir := dir
	if subdir != "" {
		clean := filepath.Clean(filepath.FromSlash(subdir))
		if filepath.ToSlash(clean) != subdir || filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			_ = os.RemoveAll(tmp)
			return nil, fmt.Errorf("invalid plugin subdirectory %q", subdir)
		}
		packageDir = filepath.Join(dir, clean)
		resolved, evalErr := filepath.EvalSymlinks(packageDir)
		resolvedRoot, rootErr := filepath.EvalSymlinks(dir)
		rel, relErr := filepath.Rel(resolvedRoot, resolved)
		if evalErr != nil || rootErr != nil || relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			_ = os.RemoveAll(tmp)
			return nil, fmt.Errorf("plugin subdirectory %q is missing or escapes the repository", subdir)
		}
		info, statErr := os.Stat(packageDir)
		if statErr != nil || !info.IsDir() {
			_ = os.RemoveAll(tmp)
			return nil, fmt.Errorf("plugin subdirectory %q is not a directory", subdir)
		}
		linkInfo, linkErr := os.Lstat(packageDir)
		if linkErr != nil || linkInfo.Mode()&os.ModeSymlink != 0 {
			_ = os.RemoveAll(tmp)
			return nil, fmt.Errorf("plugin subdirectory %q cannot be a symlink", subdir)
		}
	}
	f := &Fetched{Source: source, Ref: ref, Subdir: subdir, dir: packageDir, repoDir: dir, tmpDir: tmp}
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD")
	cmd.Stdout = &out
	if cmd.Run() == nil {
		f.Commit = strings.TrimSpace(out.String())
	}
	if f.Manifest, err = LoadManifest(packageDir); err != nil {
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
	preview := f.Manifest.Preview(src)
	if f.Subdir != "" {
		preview += fmt.Sprintf("\n  package directory: %s", f.Subdir)
	}
	for _, r := range f.Manifest.Runtimes {
		preview += fmt.Sprintf("\n  runtime: %s%s", r.Command, minVersionLabel(r.MinVersion))
	}
	return preview
}

// Dir is the fetched plugin package's directory, until Discard.
func (f *Fetched) Dir() string { return f.dir }

// Discard removes the clone.
func (f *Fetched) Discard() {
	if f.tmpDir != "" {
		_ = os.RemoveAll(f.tmpDir)
		return
	}
	_ = os.RemoveAll(filepath.Dir(f.dir))
}

// Install puts a fetched plugin in place, enabled.
func (s *Store) Install(ctx context.Context, f *Fetched, hiveVersion string) (Installed, error) {
	defer f.Discard()
	if f.Registry && (!f.checksumVerified || !validReleaseMetadata(f)) {
		return Installed{}, errors.New("registry plugin install requires a checksum-verified release matching its manifest")
	}
	if err := f.Manifest.Supported(ctx, hiveVersion); err != nil {
		return Installed{}, err
	}
	unlock, err := s.lock()
	if err != nil {
		return Installed{}, err
	}
	defer unlock()
	if err := s.recoverLocked(); err != nil {
		return Installed{}, err
	}
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
	// Installing approves what the preview showed: its required capabilities.
	in := Installed{ID: id, Source: f.Source, Ref: f.Ref, Commit: f.Commit, Version: f.Version, Checksum: f.Checksum, Registry: f.Registry, Enabled: true, InstalledAt: time.Now(), Granted: Approve(f.Manifest, nil), Trust: f.Trust}
	if err := s.saveLocked(append(list, in)); err != nil {
		if rollbackErr := os.Rename(s.Dir(id), f.dir); rollbackErr != nil {
			return Installed{}, fmt.Errorf("record plugin install: %w (also failed to roll back installed files: %w)", err, rollbackErr)
		}
		return Installed{}, err
	}
	return in, nil
}

// Update atomically replaces an installed registry plugin and retains its last known-good release.
func (s *Store) Update(ctx context.Context, f *Fetched, hiveVersion string) (Installed, error) {
	defer f.Discard()
	if err := f.Manifest.Supported(ctx, hiveVersion); err != nil {
		return Installed{}, err
	}
	if !f.Registry || !f.checksumVerified || !validReleaseMetadata(f) {
		return Installed{}, errors.New("updates require a verified registry release")
	}
	unlock, err := s.lock()
	if err != nil {
		return Installed{}, err
	}
	defer unlock()
	if err := s.recoverLocked(); err != nil {
		return Installed{}, err
	}
	list, err := s.loadLocked()
	if err != nil {
		return Installed{}, err
	}
	i := slices.IndexFunc(list, func(in Installed) bool { return in.ID == f.Manifest.ID })
	if i < 0 {
		return Installed{}, fmt.Errorf("%w %q", ErrNotFound, f.Manifest.ID)
	}
	old := list[i]
	if old.Linked {
		return Installed{}, errors.New("linked plugins cannot be updated from the registry; unlink before installing a release")
	}
	if !old.Registry {
		return Installed{}, errors.New("plugin was installed from a local or Git source; registry update is unavailable")
	}
	if old.Version == "" || old.Commit == "" || old.Checksum == "" {
		return Installed{}, errors.New("installed registry release metadata is incomplete; reinstall from the catalog before updating")
	}
	if old.Commit == f.Commit {
		return old, nil
	}
	if f.Manifest.ID != old.ID {
		return Installed{}, errors.New("registry release plugin ID does not match installed plugin")
	}
	if err := os.MkdirAll(s.VersionsDir(old.ID), 0o700); err != nil {
		return Installed{}, err
	}
	backup := filepath.Join(s.VersionsDir(old.ID), safeReleaseDir(old.Version, old.Commit))
	if _, err := os.Lstat(backup); err == nil {
		return Installed{}, errors.New("target known-good release already exists; repair plugin store before updating")
	}
	next := old
	next.Source, next.Ref, next.Commit, next.Version, next.Checksum, next.Registry = f.Source, f.Ref, f.Commit, f.Version, f.Checksum, true
	next.PreviousVersion, next.PreviousRef, next.PreviousCommit, next.PreviousChecksum = old.Version, old.Ref, old.Commit, old.Checksum
	previous := old.Trust
	next.Trust, next.PreviousTrust = f.Trust, &previous
	// Updating approves the release's required capabilities (the CLI showed
	// what changed) and keeps optional grants it still asks for.
	next.Granted = Approve(f.Manifest, old.Granted)
	tx := storeTransaction{Schema: 1, PluginID: old.ID, Operation: "update", Backup: backup, Stage: f.dir, Next: next}
	if err := jsonfile.Write(filepath.Join(s.dir, ".transaction.json"), tx); err != nil {
		return Installed{}, err
	}
	if err := os.Rename(s.Dir(old.ID), backup); err != nil {
		_ = os.Remove(filepath.Join(s.dir, ".transaction.json"))
		return Installed{}, err
	}
	if err := os.Rename(f.dir, s.Dir(old.ID)); err != nil {
		_ = os.Rename(backup, s.Dir(old.ID))
		_ = os.Remove(filepath.Join(s.dir, ".transaction.json"))
		return Installed{}, err
	}
	list[i] = next
	if err := s.saveLocked(list); err != nil {
		_ = os.RemoveAll(s.Dir(old.ID))
		_ = os.Rename(backup, s.Dir(old.ID))
		_ = os.Remove(filepath.Join(s.dir, ".transaction.json"))
		return Installed{}, err
	}
	if err := os.Remove(filepath.Join(s.dir, ".transaction.json")); err != nil {
		return Installed{}, fmt.Errorf("plugin updated but transaction cleanup failed: %w", err)
	}
	return next, nil
}

func validReleaseMetadata(f *Fetched) bool {
	return f.Manifest != nil && f.Manifest.ID != "" && f.Version != "" &&
		strings.TrimPrefix(f.Manifest.Version, "v") == strings.TrimPrefix(f.Version, "v") &&
		refMatchesVersion(f.Ref, f.Version, true) &&
		len(f.Commit) == 40 && isHex(f.Commit) && len(f.Checksum) == len("sha256:")+64 &&
		strings.HasPrefix(strings.ToLower(f.Checksum), "sha256:") && isHex(strings.TrimPrefix(strings.ToLower(f.Checksum), "sha256:"))
}

// Rollback restores the retained previous registry release. Plugin data and enabled state are preserved.
func (s *Store) Rollback(id string) (Installed, error) {
	unlock, err := s.lock()
	if err != nil {
		return Installed{}, err
	}
	defer unlock()
	if err := s.recoverLocked(); err != nil {
		return Installed{}, err
	}
	list, err := s.loadLocked()
	if err != nil {
		return Installed{}, err
	}
	i := slices.IndexFunc(list, func(in Installed) bool { return in.ID == id })
	if i < 0 {
		return Installed{}, fmt.Errorf("%w %q", ErrNotFound, id)
	}
	cur := list[i]
	if cur.Linked || !cur.Registry {
		return Installed{}, errors.New("rollback is available only for registry-installed plugins")
	}
	if cur.PreviousVersion == "" || cur.PreviousCommit == "" {
		return Installed{}, errors.New("no known-good release is retained for rollback")
	}
	backup := filepath.Join(s.VersionsDir(id), safeReleaseDir(cur.PreviousVersion, cur.PreviousCommit))
	if _, err := os.Stat(backup); err != nil {
		return Installed{}, fmt.Errorf("retained release %s is unavailable: %w", cur.PreviousVersion, err)
	}
	currentDir := filepath.Join(s.VersionsDir(id), safeReleaseDir(cur.Version, cur.Commit))
	if err := os.MkdirAll(s.VersionsDir(id), 0o700); err != nil {
		return Installed{}, err
	}
	next := cur
	next.Version, next.Ref, next.Commit, next.Checksum = cur.PreviousVersion, cur.PreviousRef, cur.PreviousCommit, cur.PreviousChecksum
	next.PreviousVersion, next.PreviousRef, next.PreviousCommit, next.PreviousChecksum = cur.Version, cur.Ref, cur.Commit, cur.Checksum
	current := cur.Trust
	next.Trust, next.PreviousTrust = Trust{}, &current
	if cur.PreviousTrust != nil {
		next.Trust = *cur.PreviousTrust
	}
	tx := storeTransaction{Schema: 1, PluginID: id, Operation: "rollback", Backup: backup, Stage: currentDir, KeepStage: true, Next: next}
	if err := jsonfile.Write(filepath.Join(s.dir, ".transaction.json"), tx); err != nil {
		return Installed{}, err
	}
	if err := os.Rename(s.Dir(id), currentDir); err != nil {
		_ = os.Remove(filepath.Join(s.dir, ".transaction.json"))
		return Installed{}, err
	}
	if err := os.Rename(backup, s.Dir(id)); err != nil {
		_ = os.Rename(currentDir, s.Dir(id))
		_ = os.Remove(filepath.Join(s.dir, ".transaction.json"))
		return Installed{}, err
	}
	list[i] = next
	if err := s.saveLocked(list); err != nil {
		_ = os.Rename(s.Dir(id), backup)
		_ = os.Rename(currentDir, s.Dir(id))
		_ = os.Remove(filepath.Join(s.dir, ".transaction.json"))
		return Installed{}, err
	}
	if err := os.Remove(filepath.Join(s.dir, ".transaction.json")); err != nil {
		return Installed{}, fmt.Errorf("plugin rolled back but transaction cleanup failed: %w", err)
	}
	return next, nil
}

func safeReleaseDir(version, commit string) string {
	version = strings.NewReplacer("/", "-", "\\", "-", "..", "-").Replace(version)
	return version + "-" + commit[:min(len(commit), 12)]
}

// Link installs the plugin in dir as it is, by a symlink: changes there
// apply at the next reload (for developing a plugin).
func (s *Store) Link(ctx context.Context, dir, hiveVersion string) (Installed, *Manifest, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Installed{}, nil, err
	}
	m, err := LoadManifest(abs)
	if err != nil {
		return Installed{}, nil, err
	}
	if err := m.Supported(ctx, hiveVersion); err != nil {
		return Installed{}, nil, err
	}
	unlock, err := s.lock()
	if err != nil {
		return Installed{}, nil, err
	}
	defer unlock()
	if err := s.recoverLocked(); err != nil {
		return Installed{}, nil, err
	}
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
	in := Installed{ID: m.ID, Source: abs, Linked: true, Enabled: true, InstalledAt: time.Now(), Granted: Approve(m, nil)}
	return in, m, s.saveLocked(append(list, in))
}

// SetGranted records the capabilities the user approved for a plugin.
func (s *Store) SetGranted(id string, granted []string) (Installed, error) {
	unlock, err := s.lock()
	if err != nil {
		return Installed{}, err
	}
	defer unlock()
	if err := s.recoverLocked(); err != nil {
		return Installed{}, err
	}
	list, err := s.loadLocked()
	if err != nil {
		return Installed{}, err
	}
	i := slices.IndexFunc(list, func(in Installed) bool { return in.ID == id })
	if i < 0 {
		return Installed{}, fmt.Errorf("%w %q", ErrNotFound, id)
	}
	list[i].Granted = sortedUnique(granted)
	return list[i], s.saveLocked(list)
}

// SetTrust records what the registry now says of an installed release
// (`hive plugin doctor` checks it again).
func (s *Store) SetTrust(id string, t Trust) (Installed, error) {
	unlock, err := s.lock()
	if err != nil {
		return Installed{}, err
	}
	defer unlock()
	if err := s.recoverLocked(); err != nil {
		return Installed{}, err
	}
	list, err := s.loadLocked()
	if err != nil {
		return Installed{}, err
	}
	i := slices.IndexFunc(list, func(in Installed) bool { return in.ID == id })
	if i < 0 {
		return Installed{}, fmt.Errorf("%w %q", ErrNotFound, id)
	}
	list[i].Trust = t
	return list[i], s.saveLocked(list)
}

// SetEnabled turns a plugin on or off.
func (s *Store) SetEnabled(id string, on bool) (Installed, error) {
	unlock, err := s.lock()
	if err != nil {
		return Installed{}, err
	}
	defer unlock()
	if err := s.recoverLocked(); err != nil {
		return Installed{}, err
	}
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

// Uninstall removes a plugin's installed files (only the link, when linked).
// Plugin data is preserved unless deleteData is explicitly true.
func (s *Store) Uninstall(id string, deleteData ...bool) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.recoverLocked(); err != nil {
		return err
	}
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
	_ = os.RemoveAll(s.CacheDir(id)) // disposable by definition
	if len(deleteData) > 0 && deleteData[0] {
		for _, d := range []string{s.DataDir(id), s.ConfigDir(id), s.VersionsDir(id)} {
			if err := os.RemoveAll(d); err != nil {
				return err
			}
		}
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
