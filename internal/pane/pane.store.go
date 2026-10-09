package pane

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

// Store persists each environment's tabs and panes.
type Store interface {
	// Load returns the environment's state; an empty one when none is stored.
	Load(ctx context.Context, envID string) (State, error)
	Save(ctx context.Context, envID string, st State) error
}

// FilesystemStore keeps <root>/<env>/layout.json, next to environment.json.
type FilesystemStore struct {
	root string
}

func NewFilesystemStore(root string) *FilesystemStore { return &FilesystemStore{root: root} }

func (s *FilesystemStore) file(envID string) string {
	return filepath.Join(s.root, envID, "layout.json")
}

func (s *FilesystemStore) Load(_ context.Context, envID string) (State, error) {
	var st State
	err := jsonfile.Read(s.file(envID), &st)
	if errors.Is(err, fs.ErrNotExist) {
		return State{Schema: stateSchema}, nil
	}
	if err != nil {
		return State{}, err
	}
	return st, nil
}

func (s *FilesystemStore) Save(_ context.Context, envID string, st State) error {
	if _, err := os.Stat(filepath.Join(s.root, envID)); err != nil {
		return err // the environment is gone; do not recreate its directory
	}
	st.Schema = stateSchema
	return jsonfile.Write(s.file(envID), st)
}
