package persist

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/jsonfile"
	"github.com/admirable-oss/hive/internal/logging"
)

// FileName is the latest snapshot, in the session's storage root.
const FileName = "session.json"

// ErrNoSnapshot means no readable snapshot exists.
var ErrNoSnapshot = errors.New("persist: no snapshot")

// rotatedPattern names rotated snapshots; the time sorts as text.
const rotatedLayout = "20060102T150405.000000000Z"

// Store reads and writes a session's snapshots: <dir>/session.json and
// <dir>/snapshots/session-<time>.json.
type Store struct {
	dir  string
	keep int
	log  *slog.Logger

	mu sync.Mutex
}

// NewStore returns a store in dir that keeps the last keep rotated
// snapshots (DefaultKeep when keep < 1).
func NewStore(dir string, keep int, log *slog.Logger) *Store {
	if keep < 1 {
		keep = DefaultKeep
	}
	return &Store{dir: dir, keep: keep, log: logging.OrDiscard(log)}
}

// Path is the latest snapshot's file.
func (s *Store) Path() string { return filepath.Join(s.dir, FileName) }

func (s *Store) rotatedDir() string { return filepath.Join(s.dir, "snapshots") }

// Save writes snap as the latest snapshot. The one it replaces is kept in
// snapshots/, and the oldest rotated ones beyond the limit are removed.
// Snapshots hold paths and environment variables, so they are private.
func (s *Store) Save(snap Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap.Schema = Schema
	if err := os.MkdirAll(s.rotatedDir(), 0o700); err != nil {
		return err
	}
	path := s.Path()
	if info, err := os.Stat(path); err == nil {
		// A hard link keeps the old contents when the new file is renamed
		// over path, and there is never a moment without a session.json.
		rotated := filepath.Join(s.rotatedDir(), "session-"+info.ModTime().UTC().Format(rotatedLayout)+".json")
		if err := os.Link(path, rotated); err != nil && !errors.Is(err, fs.ErrExist) {
			s.log.Warn("could not keep the previous snapshot", "err", err)
		}
	}
	if err := jsonfile.Write(path, snap); err != nil {
		return err
	}
	return s.pruneLocked()
}

// pruneLocked removes the oldest rotated snapshots beyond the limit.
func (s *Store) pruneLocked() error {
	names, err := s.rotatedLocked()
	if err != nil {
		return err
	}
	var errs []error
	for len(names) > s.keep {
		errs = append(errs, os.Remove(filepath.Join(s.rotatedDir(), names[0])))
		names = names[1:]
	}
	return errors.Join(errs...)
}

// rotatedLocked lists rotated snapshots, oldest first.
func (s *Store) rotatedLocked() ([]string, error) {
	entries, err := os.ReadDir(s.rotatedDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if n := e.Name(); !e.IsDir() && strings.HasPrefix(n, "session-") && strings.HasSuffix(n, ".json") {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	return names, nil
}

// Rotated lists the rotated snapshots' files, newest first.
func (s *Store) Rotated() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	names, err := s.rotatedLocked()
	if err != nil {
		return nil, err
	}
	slices.Reverse(names)
	for i, n := range names {
		names[i] = filepath.Join(s.rotatedDir(), n)
	}
	return names, nil
}

// Load returns the latest readable snapshot: session.json, else the newest
// rotated one that reads. A file that cannot be decoded is moved aside to
// <file>.corrupt-<time>, so it is neither lost nor tried again. A file from
// a newer Hive is skipped and left in place.
func (s *Store) Load() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rotated, err := s.rotatedLocked()
	if err != nil {
		return Snapshot{}, err
	}
	candidates := []string{s.Path()}
	for _, n := range slices.Backward(rotated) {
		candidates = append(candidates, filepath.Join(s.rotatedDir(), n))
	}
	for _, path := range candidates {
		snap, err := read(path)
		switch {
		case err == nil:
			return snap, nil
		case errors.Is(err, fs.ErrNotExist):
			continue
		case errors.Is(err, errNewer):
			s.log.Warn("skipping a snapshot written by a newer Hive", "path", path, "err", err)
			continue
		}
		backup, berr := BackupCorrupt(path)
		if berr != nil {
			s.log.Error("unreadable snapshot could not be moved aside", "path", path, "err", err, "backup_err", berr)
			continue
		}
		s.log.Warn("moved an unreadable snapshot aside", "path", path, "backup", backup, "err", err)
	}
	return Snapshot{}, ErrNoSnapshot
}

var errNewer = errors.New("newer schema")

func read(path string) (Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return Snapshot{}, err
	}
	switch {
	case snap.Schema > Schema:
		return Snapshot{}, fmt.Errorf("%w: schema %d (this build reads up to %d)", errNewer, snap.Schema, Schema)
	case snap.Schema < 1:
		return Snapshot{}, errors.New("no schema version")
	}
	return snap, nil
}

// CorruptSuffix marks a file moved aside because it could not be read.
const CorruptSuffix = ".corrupt-"

// BackupCorrupt moves an unreadable file aside to <path>.corrupt-<time> and
// returns the new name.
func BackupCorrupt(path string) (string, error) {
	backup := path + CorruptSuffix + time.Now().UTC().Format(rotatedLayout)
	return backup, os.Rename(path, backup)
}
