package observer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

// TestStoreRefusesUnreadableCredentials: a damaged credentials file, or one
// a newer hive wrote, is reported with its path and left as it is.
func TestStoreRefusesUnreadableCredentials(t *testing.T) {
	for name, tt := range map[string]struct {
		body string
		want error
	}{
		"truncated": {`{"schema": 1, "credentials": [`, nil},
		"newer":     {`{"schema": 2, "credentials": []}`, jsonfile.ErrNewer},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "credentials.json")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := NewStore(dir).List()
			if err == nil || !strings.Contains(err.Error(), path) || (tt.want != nil && !errors.Is(err, tt.want)) {
				t.Fatalf("List() = %v; want an error naming %s", err, path)
			}
			if data, _ := os.ReadFile(path); string(data) != tt.body {
				t.Fatalf("the file changed: %s", data)
			}
		})
	}
}
