package logging

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// RotatingFile is an append-only log file that rolls over by size:
// path → path.1 → path.2 … → path.<backups>, dropping the oldest. It is safe
// for concurrent use; each Write lands whole in exactly one file.
type RotatingFile struct {
	path    string
	maxSize int64
	backups int

	mu   sync.Mutex
	f    *os.File
	size int64
}

// OpenRotating opens (or creates) path for appending. Log files may contain
// commands and paths, so they are private to the user (0600 in a 0700 dir).
func OpenRotating(path string, maxSize int64, backups int) (*RotatingFile, error) {
	if maxSize <= 0 {
		return nil, fmt.Errorf("logging: max size must be positive, got %d", maxSize)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	r := &RotatingFile{path: path, maxSize: maxSize, backups: max(backups, 0)}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		return errors.Join(err, f.Close())
	}
	r.f, r.size = f, info.Size()
	return nil
}

// Write appends p, rotating first if p would push the file past its limit.
// A record larger than the limit still goes into a fresh file on its own.
func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, os.ErrClosed
	}
	if r.size > 0 && r.size+int64(len(p)) > r.maxSize {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *RotatingFile) rotate() error {
	if err := r.f.Close(); err != nil {
		return err
	}
	r.f = nil
	if r.backups == 0 {
		if err := os.Remove(r.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return r.open()
	}
	for i := r.backups - 1; i >= 1; i-- {
		err := os.Rename(r.backup(i), r.backup(i+1))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(r.path, r.backup(1)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return r.open()
}

func (r *RotatingFile) backup(i int) string { return fmt.Sprintf("%s.%d", r.path, i) }

// Close closes the current file. Later writes fail with os.ErrClosed.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
