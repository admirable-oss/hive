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
//	<root>/<id>/workspace/
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

	env.Path = filepath.Join(dir, "workspace")
	err := os.Mkdir(env.Path, 0o755)
	if err == nil {
		err = jsonfile.Write(s.file(env.ID), env)
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return Environment{}, err
	}
	return env, nil
}

func (s *FilesystemStore) Get(_ context.Context, id string) (Environment, error) {
	var env Environment
	if err := jsonfile.Read(s.file(id), &env); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Environment{}, ErrNotFound
		}
		return Environment{}, err
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
