package plugin

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

// TestStoreLeavesANewerRecordAlone: the installed-plugins record a newer
// hive wrote is refused, so nothing is installed over what it records.
func TestStoreLeavesANewerRecordAlone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plugins")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "registry.json")
	newer := []byte(`{"schema": 2, "plugins": []}`)
	if err := os.WriteFile(path, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(dir).List(context.Background(), "v1.0.10"); !errors.Is(err, jsonfile.ErrNewer) {
		t.Fatalf("List() = %v, want ErrNewer", err)
	}
	if data, _ := os.ReadFile(path); !bytes.Equal(data, newer) {
		t.Fatalf("the record changed: %s", data)
	}
}
