package update_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/update"
)

func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// release serves a fake GitHub release v0.8.0 for linux/arm64.
func release(t *testing.T, tamper bool) update.Source {
	t.Helper()
	arc := archive(t, map[string]string{"LICENSE": "MIT", "hive": "#!new-hive"})
	sum := sha256.Sum256(arc)
	if tamper {
		arc = append(arc, 0)
	}
	name := update.Archive("v0.8.0", "linux", "arm64")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"tag_name": "v0.8.0"}`)
	})
	mux.HandleFunc("/dl/v0.8.0/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  hive_0.8.0_darwin_arm64.tar.gz\n%s  %s\n", strings.Repeat("0", 64), hex.EncodeToString(sum[:]), name)
	})
	mux.HandleFunc("/dl/v0.8.0/"+name, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(arc) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return update.Source{API: srv.URL + "/api", Download: srv.URL + "/dl"}
}

func TestFetchVerifiesAndExtracts(t *testing.T) {
	ctx := context.Background()
	src := release(t, false)
	tag, err := src.Latest(ctx)
	if err != nil || tag != "v0.8.0" {
		t.Fatalf("latest = %q, %v", tag, err)
	}
	bin, err := src.Fetch(ctx, tag, "linux", "arm64")
	if err != nil || string(bin) != "#!new-hive" {
		t.Fatalf("fetch = %q, %v", bin, err)
	}
	if _, err := src.Fetch(ctx, tag, "freebsd", "arm64"); err == nil || !strings.Contains(err.Error(), "lists no") {
		t.Fatalf("a system without a build: %v", err)
	}
}

func TestFetchRefusesATamperedArchive(t *testing.T) {
	if _, err := release(t, true).Fetch(context.Background(), "v0.8.0", "linux", "arm64"); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("tampered archive: %v", err)
	}
}

func TestInstallReplacesTheBinary(t *testing.T) {
	target := filepath.Join(t.TempDir(), "hive")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A running process keeps the old file open; it must keep its contents.
	old, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if err := update.Install(target, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(target); string(data) != "new" {
		t.Fatalf("target = %q", data)
	}
	if info, _ := os.Stat(target); info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	buf := make([]byte, 3)
	if _, err := old.ReadAt(buf, 0); err != nil || string(buf) != "old" {
		t.Fatalf("the old binary changed under its reader: %q, %v", buf, err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(target)); len(entries) != 1 {
		t.Fatalf("temporary files left: %v", entries)
	}
}

func TestNewer(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want bool
	}{
		{"v0.8.0", "v0.7.3", true},
		{"v0.7.3", "v0.8.0", false},
		{"v0.8.0", "0.8.0", false},
		{"v1.0.0", "v1.0.0-rc.2", true},
		{"v1.0.0-rc.2", "v1.0.0-rc.1", true},
		{"v1.0.0-rc.1", "v1.0.0", false},
		{"v0.8.0", "dev", true},
		{"dev", "v0.8.0", false},
		{"v0.10.0", "v0.9.9", true},
	} {
		if got := update.Newer(tt.a, tt.b); got != tt.want {
			t.Errorf("Newer(%q, %q) = %v", tt.a, tt.b, got)
		}
	}
}

func TestManaged(t *testing.T) {
	if update.Managed("/opt/homebrew/Cellar/hive/0.7.0/bin/hive") != "Homebrew" || update.Managed("/nix/store/abc-hive/bin/hive") != "Nix" || update.Managed("/home/me/.local/bin/hive") != "" {
		t.Fatal("package managers not recognised")
	}
}
