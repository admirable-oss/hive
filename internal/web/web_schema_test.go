package web

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

// TestStoreLeavesNewerFilesAlone: runtimes a newer hive wrote are refused,
// not misread or overwritten.
func TestStoreLeavesNewerFilesAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtimes.json")
	newer := []byte(`{"schema": 2, "runtimes": []}`)
	if err := os.WriteFile(path, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(dir)
	if _, err := s.Runtimes(); !errors.Is(err, jsonfile.ErrNewer) {
		t.Fatalf("Runtimes() = %v, want ErrNewer", err)
	}
	if data, _ := os.ReadFile(path); !bytes.Equal(data, newer) {
		t.Fatalf("the file changed: %s", data)
	}
}
