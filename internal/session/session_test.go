package session_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/goleak"

	"github.com/admirable-oss/hive/internal/session"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestRoot(t *testing.T) {
	tests := []struct{ name, want string }{
		{"", "/b"},
		{"default", "/b"},
		{"work", "/b/sessions/work"},
		{"ci.run-2", "/b/sessions/ci.run-2"},
	}
	for _, tt := range tests {
		got, err := session.Root("/b", tt.name)
		if err != nil || got != tt.want {
			t.Errorf("Root(%q) = %q, %v; want %q", tt.name, got, err, tt.want)
		}
	}
	for _, bad := range []string{"../x", ".hidden", "a/b", "sp ace"} {
		if _, err := session.Root("/b", bad); !errors.Is(err, session.ErrInvalidName) {
			t.Errorf("Root(%q) should be rejected, got %v", bad, err)
		}
	}
}

func TestList(t *testing.T) {
	base := t.TempDir()
	got, err := session.List(base)
	if err != nil || len(got) != 1 || got[0].Name != "default" || got[0].Root != base {
		t.Fatalf("empty base: %+v, %v", got, err)
	}
	for _, n := range []string{"zeta", "alpha", ".skip"} {
		_ = os.MkdirAll(filepath.Join(base, "sessions", n), 0o700)
	}
	_ = os.WriteFile(filepath.Join(base, "sessions", "file"), nil, 0o600)
	got, err = session.List(base)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, i := range got {
		names = append(names, i.Name)
	}
	if len(names) != 3 || names[0] != "default" || names[1] != "alpha" || names[2] != "zeta" {
		t.Fatalf("names = %v", names)
	}
}
