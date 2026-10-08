// Package jsonfile reads and writes JSON documents on disk. It is the shared
// persistence primitive behind the filesystem stores.
package jsonfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Write stores v at path atomically and durably: it writes a temp file in the
// same directory, syncs it, renames it into place and syncs the directory.
// Readers never see a torn document, and a crash right after Write returns
// cannot roll the file back to its previous contents.
func Write(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once the rename succeeded

	if _, err := tmp.Write(data); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if err := tmp.Sync(); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return syncDir(dir)
}

// syncDir makes a rename inside dir durable. Some filesystems refuse to sync
// a directory; that only weakens durability, so it is not reported.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	_ = d.Sync()
	return d.Close()
}

// Read decodes the JSON document at path into v.
func Read(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
