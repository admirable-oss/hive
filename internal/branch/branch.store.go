package branch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

// Store keeps the branches between daemon runs.
type Store interface {
	Load() ([]Branch, error)
	Save([]Branch) error
}

// FilesystemStore keeps every branch in one file, <dir>/branches.json. A
// file from a newer hive is left alone (and nothing is saved over it); an
// unreadable one is moved aside to branches.json.corrupt-<time>.
type FilesystemStore struct {
	path  string
	newer bool // the file is from a newer hive: never write it
}

// NewFilesystemStore keeps branches in dir.
func NewFilesystemStore(dir string) *FilesystemStore {
	return &FilesystemStore{path: filepath.Join(dir, "branches.json")}
}

type document struct {
	Schema   int      `json:"schema"`
	Branches []Branch `json:"branches"`
}

func (s *FilesystemStore) Load() ([]Branch, error) {
	var doc document
	err := jsonfile.Read(s.path, &doc)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		backup := s.path + ".corrupt-" + time.Now().UTC().Format("20060102T150405Z")
		if rerr := os.Rename(s.path, backup); rerr != nil {
			return nil, fmt.Errorf("%s: %w (and it could not be moved aside: %w)", s.path, err, rerr)
		}
		return nil, fmt.Errorf("%s: %w; moved to %s", s.path, err, backup)
	}
	if err := jsonfile.CheckSchema(s.path, doc.Schema, schema); err != nil {
		s.newer = true
		return nil, fmt.Errorf("%w: %w", ErrNewer, err)
	}
	return doc.Branches, nil
}

func (s *FilesystemStore) Save(branches []Branch) error {
	if s.newer {
		return fmt.Errorf("%w: %s is left as it is", ErrNewer, s.path)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	if branches == nil {
		branches = []Branch{}
	}
	return jsonfile.Write(s.path, document{Schema: schema, Branches: branches})
}
