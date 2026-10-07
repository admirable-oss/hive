package environment

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

type FilesystemStore struct {
	baseDir string
}

func NewFilesystemStore(baseDir string) *FilesystemStore {
	return &FilesystemStore{baseDir: baseDir}
}

func (s *FilesystemStore) envDir(id string) string {
	return filepath.Join(s.baseDir, id)
}

func (s *FilesystemStore) envFile(id string) string {
	return filepath.Join(s.envDir(id), "environment.json")
}

func (s *FilesystemStore) Create(ctx context.Context, env Environment) error {
	dir := s.envDir(env.ID)
	if _, err := os.Stat(dir); err == nil {
		return ErrAlreadyExists
	}
	if err := os.MkdirAll(filepath.Join(dir, "workspace"), 0755); err != nil {
		return err
	}
	
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.envFile(env.ID), data, 0644)
}

func (s *FilesystemStore) Get(ctx context.Context, id string) (Environment, error) {
	var env Environment
	data, err := os.ReadFile(s.envFile(id))
	if err != nil {
		if os.IsNotExist(err) {
			return env, ErrNotFound
		}
		return env, err
	}
	err = json.Unmarshal(data, &env)
	return env, err
}

func (s *FilesystemStore) List(ctx context.Context) ([]Environment, error) {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Environment{}, nil
		}
		return nil, err
	}

	var envs []Environment
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		env, err := s.Get(ctx, entry.Name())
		if err == nil {
			envs = append(envs, env)
		}
	}
	return envs, nil
}

func (s *FilesystemStore) Delete(ctx context.Context, id string) error {
	dir := s.envDir(id)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return ErrNotFound
	}
	return os.RemoveAll(dir)
}
