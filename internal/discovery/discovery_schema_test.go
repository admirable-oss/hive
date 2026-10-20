package discovery

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

// TestLoadIdentityLeavesANewerFileAlone: an identity a newer hive wrote is
// refused, never replaced by a new one (paired machines would see the
// identity change).
func TestLoadIdentityLeavesANewerFileAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, IdentityFile)
	newer := []byte(`{"schema": 2, "keys": []}`)
	if err := os.WriteFile(path, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentity(dir); !errors.Is(err, jsonfile.ErrNewer) {
		t.Fatalf("LoadIdentity() = %v, want ErrNewer", err)
	}
	if data, _ := os.ReadFile(path); !bytes.Equal(data, newer) {
		t.Fatalf("the identity changed: %s", data)
	}
}
