package workflow

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

// Store keeps runs, trust and trigger state:
//
//	<dir>/runs/<run-id>.json
//	<dir>/trust.json     the shared files trusted, by path and digest
//	<dir>/state.json     what triggers saw: git references, every's last run
//	<dir>/out/           commands' $HIVE_OUTPUT files while they run
//
// A file from a newer hive is left alone (and never written over); an
// unreadable one is moved aside to <name>.corrupt-<time>.
type Store interface {
	ListRuns(ctx context.Context) ([]Run, error)
	PutRun(ctx context.Context, r *Run) error
	DeleteRun(ctx context.Context, id string) error
	LoadTrust() (map[string]string, error)
	SaveTrust(files map[string]string) error
	LoadState() (TriggerState, error)
	SaveState(st TriggerState) error
	// OutputFile returns a new, empty file for a command's outputs.
	OutputFile(name string) (string, error)
}

// TriggerState is what triggers remember across restarts.
type TriggerState struct {
	Schema int `json:"schema"`
	// Refs are each watched repository's references and their commits.
	Refs map[string]map[string]string `json:"refs,omitempty"`
	// Every is when each every trigger ("<workflow>#<index>") last fired.
	Every map[string]time.Time `json:"every,omitempty"`
}

type trustFile struct {
	Schema int               `json:"schema"`
	Files  map[string]string `json:"files"`
}

// FilesystemStore is the Store on disk.
type FilesystemStore struct {
	dir string
	// readOnly names files a newer hive wrote, which are never written
	// over.
	readOnly map[string]bool
}

// NewFilesystemStore keeps everything under dir.
func NewFilesystemStore(dir string) *FilesystemStore {
	return &FilesystemStore{dir: dir, readOnly: map[string]bool{}}
}

func (s *FilesystemStore) runsDir() string          { return filepath.Join(s.dir, "runs") }
func (s *FilesystemStore) runFile(id string) string { return filepath.Join(s.runsDir(), id+".json") }

// ListRuns reads every run file.
func (s *FilesystemStore) ListRuns(ctx context.Context) ([]Run, error) {
	entries, err := os.ReadDir(s.runsDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var runs []Run
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
			continue
		}
		if ctx.Err() != nil {
			return runs, ctx.Err()
		}
		path := filepath.Join(s.runsDir(), name)
		var r Run
		if err := s.read(path, &r, func() int { return r.Schema }); err != nil {
			errs = append(errs, err)
			continue
		}
		if want := strings.TrimSuffix(name, ".json"); r.ID != want {
			errs = append(errs, fmt.Errorf("%s: names run %q", path, r.ID))
			continue
		}
		runs = append(runs, r)
	}
	return runs, errors.Join(errs...)
}

// read decodes path into v, moving an unreadable file aside and refusing
// a newer schema.
func (s *FilesystemStore) read(path string, v any, schemaOf func() int) error {
	if err := jsonfile.Read(path, v); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return err
		}
		backup := path + ".corrupt-" + time.Now().UTC().Format("20060102T150405Z")
		if rerr := os.Rename(path, backup); rerr != nil {
			return fmt.Errorf("%s: %w (and it could not be moved aside: %w)", path, err, rerr)
		}
		return fmt.Errorf("%s: %w; moved to %s", path, err, backup)
	}
	if err := jsonfile.CheckSchema(path, schemaOf(), schema); err != nil {
		s.readOnly[path] = true
		return fmt.Errorf("%w: %w", ErrNewer, err)
	}
	return nil
}

// PutRun writes a run atomically.
func (s *FilesystemStore) PutRun(_ context.Context, r *Run) error {
	if err := os.MkdirAll(s.runsDir(), 0o700); err != nil {
		return err
	}
	r.Schema = schema
	return jsonfile.Write(s.runFile(r.ID), r)
}

// DeleteRun removes a run's file; a missing one is not an error.
func (s *FilesystemStore) DeleteRun(_ context.Context, id string) error {
	if err := os.Remove(s.runFile(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (s *FilesystemStore) trustPath() string { return filepath.Join(s.dir, "trust.json") }
func (s *FilesystemStore) statePath() string { return filepath.Join(s.dir, "state.json") }

// LoadTrust reads the trusted files; none is an empty map.
func (s *FilesystemStore) LoadTrust() (map[string]string, error) {
	var f trustFile
	err := s.read(s.trustPath(), &f, func() int { return f.Schema })
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return map[string]string{}, err
	}
	if f.Files == nil {
		f.Files = map[string]string{}
	}
	return f.Files, nil
}

// SaveTrust writes the trusted files.
func (s *FilesystemStore) SaveTrust(files map[string]string) error {
	if s.readOnly[s.trustPath()] {
		return fmt.Errorf("%w: %s (not written over)", ErrNewer, s.trustPath())
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return jsonfile.Write(s.trustPath(), trustFile{Schema: schema, Files: files})
}

// LoadState reads what triggers saw; none is empty.
func (s *FilesystemStore) LoadState() (TriggerState, error) {
	var st TriggerState
	err := s.read(s.statePath(), &st, func() int { return st.Schema })
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	if st.Refs == nil {
		st.Refs = map[string]map[string]string{}
	}
	if st.Every == nil {
		st.Every = map[string]time.Time{}
	}
	return st, err
}

// SaveState writes what triggers saw.
func (s *FilesystemStore) SaveState(st TriggerState) error {
	if s.readOnly[s.statePath()] {
		return nil // a newer hive's; it keeps its own
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	st.Schema = schema
	return jsonfile.Write(s.statePath(), st)
}

// OutputFile creates an empty file for a command's outputs.
func (s *FilesystemStore) OutputFile(name string) (string, error) {
	dir := filepath.Join(s.dir, "out")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	return path, f.Close()
}
