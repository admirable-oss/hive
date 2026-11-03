package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

// Store keeps the trusted project files: <dir>/trust.json, each project's
// root and the digest of the text trusted. A file from a newer hive is
// left alone and never written over; an unreadable one is moved aside.
type Store struct {
	dir      string
	readOnly bool
}

type trustFile struct {
	Schema   int               `json:"schema"`
	Projects map[string]string `json:"projects"`
}

// NewStore keeps trust under dir.
func NewStore(dir string) *Store { return &Store{dir: dir} }

func (s *Store) path() string { return filepath.Join(s.dir, "trust.json") }

// Load reads the trusted projects; none is an empty map.
func (s *Store) Load() (map[string]string, error) {
	var f trustFile
	err := jsonfile.Read(s.path(), &f)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return map[string]string{}, nil
	case err != nil:
		backup := s.path() + ".corrupt-" + time.Now().UTC().Format("20060102T150405Z")
		if rerr := os.Rename(s.path(), backup); rerr != nil {
			return map[string]string{}, fmt.Errorf("%s: %w (and it could not be moved aside: %w)", s.path(), err, rerr)
		}
		return map[string]string{}, fmt.Errorf("%s: %w; moved to %s", s.path(), err, backup)
	}
	if err := jsonfile.CheckSchema(s.path(), f.Schema, schema); err != nil {
		s.readOnly = true
		return map[string]string{}, fmt.Errorf("%w: %w", ErrNewer, err)
	}
	if f.Projects == nil {
		f.Projects = map[string]string{}
	}
	return f.Projects, nil
}

// Save writes the trusted projects.
func (s *Store) Save(projects map[string]string) error {
	if s.readOnly {
		return fmt.Errorf("%w: %s (not written over)", ErrNewer, s.path())
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return jsonfile.Write(s.path(), trustFile{Schema: schema, Projects: projects})
}
