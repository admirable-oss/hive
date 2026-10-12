package config_test

import (
	"os"
	"testing"

	"github.com/admirable-oss/hive/internal/apisurface"
	"github.com/admirable-oss/hive/internal/config"
)

// TestConfigKeysFrozen holds the configuration keys to api/v1/config.txt:
// a key that disappears would make users' files warn.
func TestConfigKeysFrozen(t *testing.T) {
	apisurface.Check(t, "../../api/v1/config.txt", config.KeyNames())
}

// TestReferenceUpToDate keeps docs/config.md generated from the code.
func TestReferenceUpToDate(t *testing.T) {
	const path = "../../docs/config.md"
	want := config.Reference()
	if os.Getenv(apisurface.UpdateEnv) == "1" {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("docs/config.md is out of date: HIVE_UPDATE_SURFACE=1 go test ./internal/config -run Reference (%v)", err)
	}
}
