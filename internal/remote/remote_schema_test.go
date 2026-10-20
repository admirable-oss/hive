package remote

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

// TestRegistryLeavesANewerFileAlone: machines.json written by a newer hive
// (after a rollback) is refused, not misread or overwritten.
func TestRegistryLeavesANewerFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machines.json")
	newer := []byte(`{"schema": 2, "machines": [{"name": "box", "host": "box", "future": true}]}`)
	if err := os.WriteFile(path, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(path)
	if _, err := r.List(); !errors.Is(err, jsonfile.ErrNewer) {
		t.Fatalf("List() = %v, want ErrNewer", err)
	}
	if _, err := r.Add(Machine{Name: "other", Host: "other"}); err == nil {
		t.Fatal("Add() wrote over a newer file")
	}
	if data, _ := os.ReadFile(path); !bytes.Equal(data, newer) {
		t.Fatalf("the file changed: %s", data)
	}
}
