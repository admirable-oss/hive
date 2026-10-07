// Package jsonfile reads and writes JSON documents on disk. It is the shared
// persistence primitive behind the filesystem stores.
package jsonfile

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Write stores v at path atomically: it writes a temp file in the same
// directory and renames it into place, so readers never see a torn document.
func Write(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once the rename succeeded

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Read decodes the JSON document at path into v.
func Read(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
