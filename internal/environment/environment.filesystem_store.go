package environment

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

// FilesystemStore keeps one directory per environment:
//
//	<root>/<id>/environment.json
//	<root>/<id>/workspace/        only for managed environments
//
// Deleting an environment removes <root>/<id> only. An environment rooted in
// the user's own directory keeps nothing there, so that directory is never
// touched.
type FilesystemStore struct {
	root string
}

func NewFilesystemStore(root string) *FilesystemStore {
	return &FilesystemStore{root: root}
}

func (s *FilesystemStore) dir(id string) string  { return filepath.Join(s.root, id) }
func (s *FilesystemStore) file(id string) string { return filepath.Join(s.dir(id), "environment.json") }

func (s *FilesystemStore) Create(_ context.Context, env Environment) (Environment, error) {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return Environment{}, err
	}
	// Mkdir (not MkdirAll) fails atomically if the environment already exists.
	dir := s.dir(env.ID)
	if err := os.Mkdir(dir, 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return Environment{}, ErrAlreadyExists
		}
		return Environment{}, err
	}

	var err error
	if env.Path == "" {
		env.Path, env.Managed = filepath.Join(dir, "workspace"), true
		err = os.Mkdir(env.Path, 0o755)
	}
	if err == nil {
		err = jsonfile.Write(s.file(env.ID), stored(env))
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return Environment{}, err
	}
	return env, nil
}

func (s *FilesystemStore) Update(_ context.Context, env Environment) error {
	if _, err := os.Stat(s.file(env.ID)); errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	return jsonfile.Write(s.file(env.ID), stored(env))
}

// stored is env as written to disk: current schema, no live state.
func stored(env Environment) Environment {
	env.Schema, env.Git = schema, nil
	return env
}

func (s *FilesystemStore) Get(_ context.Context, id string) (Environment, error) {
	var env Environment
	if err := jsonfile.Read(s.file(id), &env); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Environment{}, ErrNotFound
		}
		return Environment{}, err
	}
	if env.Schema == 0 {
		// Before schema 1 every environment had a managed workspace.
		env.Schema, env.Managed = schema, true
	}
	return env, nil
}

// List returns environments sorted by ID. Unreadable entries are skipped so
// one corrupt directory cannot hide the rest.
func (s *FilesystemStore) List(ctx context.Context) ([]Environment, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	envs := []Environment{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if env, err := s.Get(ctx, entry.Name()); err == nil {
			envs = append(envs, env)
		}
	}
	return envs, nil
}

func (s *FilesystemStore) Delete(_ context.Context, id string) error {
	dir := s.dir(id)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	return os.RemoveAll(dir)
}
