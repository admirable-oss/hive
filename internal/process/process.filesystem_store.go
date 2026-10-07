package process

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

func (s *FilesystemStore) processDir(envID, processID string) string {
	return filepath.Join(s.baseDir, envID, "processes", processID)
}

func (s *FilesystemStore) processFile(envID, processID string) string {
	return filepath.Join(s.processDir(envID, processID), "process.json")
}

func writeFileAtomic(filename string, data []byte) error {
	dir := filepath.Dir(filename)
	tmpFile, err := os.CreateTemp(dir, "tmp-*.json")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filename)
}

func (s *FilesystemStore) Create(ctx context.Context, p Process) error {
	dir := s.processDir(p.EnvironmentID, p.ID)
	if _, err := os.Stat(dir); err == nil {
		return ErrAlreadyExists
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	stdoutFile := filepath.Join(dir, "stdout.log")
	if _, err := os.Stat(stdoutFile); os.IsNotExist(err) {
		_ = os.WriteFile(stdoutFile, []byte{}, 0644)
	}
	stderrFile := filepath.Join(dir, "stderr.log")
	if _, err := os.Stat(stderrFile); os.IsNotExist(err) {
		_ = os.WriteFile(stderrFile, []byte{}, 0644)
	}

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.processFile(p.EnvironmentID, p.ID), data)
}

func (s *FilesystemStore) Update(ctx context.Context, p Process) error {
	file := s.processFile(p.EnvironmentID, p.ID)
	if _, err := os.Stat(file); os.IsNotExist(err) {
		return ErrNotFound
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(file, data)
}

func (s *FilesystemStore) Get(ctx context.Context, id string) (Process, error) {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return Process{}, ErrNotFound
		}
		return Process{}, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		file := s.processFile(entry.Name(), id)
		data, err := os.ReadFile(file)
		if err == nil {
			var p Process
			if err := json.Unmarshal(data, &p); err == nil {
				return p, nil
			}
		}
	}

	return Process{}, ErrNotFound
}

func (s *FilesystemStore) List(ctx context.Context, envID string) ([]Process, error) {
	processesDir := filepath.Join(s.baseDir, envID, "processes")
	entries, err := os.ReadDir(processesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Process{}, nil
		}
		return nil, err
	}

	var processes []Process
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		file := filepath.Join(processesDir, entry.Name(), "process.json")
		data, err := os.ReadFile(file)
		if err == nil {
			var p Process
			if err := json.Unmarshal(data, &p); err == nil {
				processes = append(processes, p)
			}
		}
	}
	return processes, nil
}
