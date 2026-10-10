package pane

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/admirable-oss/hive/internal/jsonfile"
	"github.com/admirable-oss/hive/internal/logging"
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
	// Recover supplies an environment's state when its layout.json cannot
	// be read (it is moved aside to layout.json.corrupt-<time> first): the
	// last session snapshot's. Nil, or nothing to recover, starts empty.
	Recover func(envID string) (State, bool)
	Log     *slog.Logger
}

func NewFilesystemStore(root string) *FilesystemStore { return &FilesystemStore{root: root} }

func (s *FilesystemStore) file(envID string) string {
	return filepath.Join(s.root, envID, "layout.json")
}

func (s *FilesystemStore) Load(ctx context.Context, envID string) (State, error) {
	data, err := os.ReadFile(s.file(envID))
	if errors.Is(err, fs.ErrNotExist) {
		return State{Schema: stateSchema}, nil
	}
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return s.recover(ctx, envID, err)
	}
	return st, nil
}

// recover moves an unreadable layout.json aside and starts over from the
// last snapshot, so one damaged file cannot take every pane down with it.
func (s *FilesystemStore) recover(ctx context.Context, envID string, cause error) (State, error) {
	log := logging.OrDiscard(s.Log).With("env", envID)
	path := s.file(envID)
	backup := path + ".corrupt-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if err := os.Rename(path, backup); err != nil {
		return State{}, errors.Join(cause, err)
	}
	st, ok := State{Schema: stateSchema}, false
	if s.Recover != nil {
		st, ok = s.Recover(envID)
	}
	if err := s.Save(ctx, envID, st); err != nil {
		return State{}, err
	}
	log.Warn("layout.json could not be read; moved it aside", "backup", backup, "err", cause, "restored_from_snapshot", ok, "tabs", len(st.Tabs))
	return st, nil
}

func (s *FilesystemStore) Save(_ context.Context, envID string, st State) error {
	if _, err := os.Stat(filepath.Join(s.root, envID)); err != nil {
		return err // the environment is gone; do not recreate its directory
	}
	st.Schema = stateSchema
	return jsonfile.Write(s.file(envID), st)
}
