package update_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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

// release serves a fake GitHub release v0.8.0 for linux/arm64, its
// checksums signed by key (unsigned when key is nil).
func release(t *testing.T, tamper bool) update.Source { return signedRelease(t, tamper, nil) }

func signedRelease(t *testing.T, tamper bool, key ed25519.PrivateKey) update.Source {
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
	sums := fmt.Sprintf("%s  hive_0.8.0_darwin_arm64.tar.gz\n%s  %s\n", strings.Repeat("0", 64), hex.EncodeToString(sum[:]), name)
	mux.HandleFunc("/dl/v0.8.0/checksums.txt", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, sums) })
	if key != nil {
		mux.HandleFunc("/dl/v0.8.0/checksums.txt.sig", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(sums))))
		})
	}
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
	// (Windows: a running .exe can be renamed, but a file Go opened cannot,
	// so there the reader is left out.)
	var old *os.File
	if runtime.GOOS != "windows" {
		var err error
		if old, err = os.Open(target); err != nil {
			t.Fatal(err)
		}
		defer old.Close()
	}
	if err := update.Install(target, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(target); string(data) != "new" {
		t.Fatalf("target = %q", data)
	}
	if old != nil {
		if info, _ := os.Stat(target); info.Mode().Perm() != 0o755 {
			t.Fatalf("mode = %v", info.Mode().Perm())
		}
		buf := make([]byte, 3)
		if _, err := old.ReadAt(buf, 0); err != nil || string(buf) != "old" {
			t.Fatalf("the old binary changed under its reader: %q, %v", buf, err)
		}
	}
	if entries, _ := os.ReadDir(filepath.Dir(target)); len(entries) != 2 {
		t.Fatalf("want the binary and the one it replaced, got %v", entries)
	}
	if data, _ := os.ReadFile(update.Previous(target)); string(data) != "old" {
		t.Fatalf("previous = %q", data)
	}
}

// TestRollbackSwapsBack: a rollback puts the previous binary back and keeps
// the newer one, so it can be undone the same way.
func TestRollbackSwapsBack(t *testing.T) {
	target := filepath.Join(t.TempDir(), "hive")
	if err := update.Rollback(target); err == nil {
		t.Fatal("rolled back with nothing to roll back to")
	}
	if err := os.WriteFile(target, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := update.Install(target, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if err := update.Rollback(target); err != nil {
		t.Fatal(err)
	}
	cur, _ := os.ReadFile(target)
	prev, _ := os.ReadFile(update.Previous(target))
	if string(cur) != "v1" || string(prev) != "v2" {
		t.Fatalf("after rollback: current %q, previous %q", cur, prev)
	}
	if err := update.Install(target, []byte("v3")); err != nil {
		t.Fatal(err)
	}
	if prev, _ := os.ReadFile(update.Previous(target)); string(prev) != "v1" {
		t.Fatalf("an update keeps the binary it replaced: %q", prev)
	}
}

func TestExtractZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"LICENSE": "mit", "hive.exe": "MZ-windows-binary"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	bin, err := update.Extract(buf.Bytes())
	if err != nil || string(bin) != "MZ-windows-binary" {
		t.Fatalf("extract = %q, %v", bin, err)
	}
	if got := update.Archive("v1.2.3", "windows", "amd64"); got != "hive_1.2.3_windows_amd64.zip" {
		t.Fatalf("archive name %q", got)
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
		{"v1.0.10", "v1.0.9", true},
		{"v1.0.0-rc.10", "v1.0.0-rc.9", true},
		{"v1.0.0-rc.9", "v1.0.0-rc.10", false},
		{"v1.0.0-rc.1.1", "v1.0.0-rc.1", true},
		{"v1.0.0-rc", "v1.0.0-rc.1", false},
		{"v1.0.0-rc.1", "v1.0.0-2", true}, // alphanumeric after numeric
		{"v1.0.0-beta", "v1.0.0-alpha", true},
		{"v1.0.0-rc.1", "v1.0.0-rc.1", false},
	} {
		if got := update.Newer(tt.a, tt.b); got != tt.want {
			t.Errorf("Newer(%q, %q) = %v", tt.a, tt.b, got)
		}
	}
}

func TestManaged(t *testing.T) {
	for _, tt := range []struct{ path, manager, upgrade string }{
		{"/opt/homebrew/Cellar/hive-agents/1.0.10/bin/hive", "Homebrew", "brew upgrade hive-agents"},
		{"/usr/local/Cellar/hive-agents/1.0.10/bin/hive", "Homebrew", "brew upgrade hive-agents"},
		{"/opt/homebrew/Caskroom/hive/1.0.10/hive", "Homebrew", "brew upgrade --cask hive"},
		{"/nix/store/abc-hive/bin/hive", "Nix", ""},
		{"/home/me/.local/bin/hive", "", ""},
	} {
		if got := update.Managed(tt.path); got != tt.manager {
			t.Errorf("Managed(%s) = %q, want %q", tt.path, got, tt.manager)
		}
		if got := update.UpgradeCommand(tt.path); got != tt.upgrade {
			t.Errorf("UpgradeCommand(%s) = %q, want %q", tt.path, got, tt.upgrade)
		}
	}
}

func TestFetchVerifiesTheReleaseSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	ctx := context.Background()

	src := signedRelease(t, false, priv)
	src.PublicKey = pub
	if bin, err := src.Fetch(ctx, "v0.8.0", "linux", "arm64"); err != nil || string(bin) != "#!new-hive" {
		t.Fatalf("signed release: %q, %v", bin, err)
	}
	forged := signedRelease(t, false, other)
	forged.PublicKey = pub
	if _, err := forged.Fetch(ctx, "v0.8.0", "linux", "arm64"); !errors.Is(err, update.ErrUnsigned) {
		t.Fatalf("signed by another key: %v", err)
	}
	unsigned := release(t, false)
	unsigned.PublicKey = pub
	if _, err := unsigned.Fetch(ctx, "v0.8.0", "linux", "arm64"); !errors.Is(err, update.ErrUnsigned) {
		t.Fatalf("unsigned release: %v", err)
	}
	if _, err := update.ParseKey(base64.StdEncoding.EncodeToString(pub)); err != nil {
		t.Fatal(err)
	}
	if _, err := update.ParseKey("not a key"); err == nil {
		t.Fatal("parsed a bad key")
	}
}

func TestPreviewChannelTakesPreReleases(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/releases/latest", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"tag_name":"v0.9.0"}`) })
	mux.HandleFunc("/api/releases", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"tag_name":"v1.1.0","draft":true},{"tag_name":"v1.0.0-rc.2"},{"tag_name":"v0.9.0"},{"tag_name":"v1.0.0-rc.1"}]`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	src := update.Source{API: srv.URL + "/api"}
	ctx := context.Background()
	if tag, err := src.LatestOn(ctx, "stable"); err != nil || tag != "v0.9.0" {
		t.Fatalf("stable = %q, %v", tag, err)
	}
	if tag, err := src.LatestOn(ctx, "preview"); err != nil || tag != "v1.0.0-rc.2" {
		t.Fatalf("preview = %q, %v", tag, err)
	}
	if _, err := src.LatestOn(ctx, "nightly"); err == nil {
		t.Fatal("unknown channel accepted")
	}
}

// TestStableBeforeFirstRelease: until a stable release exists, GitHub has
// no latest release and stable takes the newest release candidate.
func TestStableBeforeFirstRelease(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/releases/latest", http.NotFound)
	mux.HandleFunc("/api/releases", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"tag_name":"v1.3.0-rc.4","draft":true},{"tag_name":"v1.3.0-rc.3"},{"tag_name":"v1.3.0-rc.2"}]`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	src := update.Source{API: srv.URL + "/api"}
	if tag, err := src.LatestOn(context.Background(), "stable"); err != nil || tag != "v1.3.0-rc.3" {
		t.Fatalf("stable = %q, %v", tag, err)
	}
}

// TestFetchFailsSafely: a release that cannot be verified or read installs
// nothing, and says why.
func TestFetchFailsSafely(t *testing.T) {
	ctx := context.Background()
	name := update.Archive("v1.0.10", "linux", "amd64")
	serve := func(t *testing.T, arc []byte, sums string) update.Source {
		t.Helper()
		mux := http.NewServeMux()
		mux.HandleFunc("/dl/v1.0.10/checksums.txt", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, sums) })
		mux.HandleFunc("/dl/v1.0.10/"+name, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(arc) })
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		return update.Source{Download: srv.URL + "/dl"}
	}
	sumOf := func(arc []byte) string {
		s := sha256.Sum256(arc)
		return hex.EncodeToString(s[:]) + "  " + name + "\n"
	}
	good := archive(t, map[string]string{"hive": "#!hive"})
	for _, tt := range []struct {
		name string
		arc  []byte
		sums string
		want string
	}{
		{"a truncated archive", good[:len(good)/2], sumOf(good), "checksum"},
		{"a truncated archive listed with its own checksum", good[:len(good)/2], sumOf(good[:len(good)/2]), "read the archive"},
		{"an archive without a hive binary", archive(t, map[string]string{"README": "x"}), sumOf(archive(t, map[string]string{"README": "x"})), "no hive binary"},
		{"no checksum for this system", good, strings.Repeat("0", 64) + "  hive_1.0.10_darwin_arm64.tar.gz\n", "lists no"},
		{"a malformed checksum line", good, "abc  " + name + "\n", "lists no"},
		{"no checksums at all", good, "", "lists no"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bin, err := serve(t, tt.arc, tt.sums).Fetch(ctx, "v1.0.10", "linux", "amd64")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("fetch = %q, %v; want an error about %q", bin, err, tt.want)
			}
		})
	}

	t.Run("a missing release", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		defer srv.Close()
		if _, err := (update.Source{Download: srv.URL}).Fetch(ctx, "v1.0.10", "linux", "amd64"); err == nil {
			t.Fatal("fetched a release that does not exist")
		}
	})
	t.Run("an archive larger than the limit", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/dl/v1.0.10/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, strings.Repeat("0", 64)+"  "+name+"\n")
		})
		mux.HandleFunc("/dl/v1.0.10/"+name, func(w http.ResponseWriter, _ *http.Request) {
			chunk := make([]byte, 1<<20)
			for range 257 {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		if _, err := (update.Source{Download: srv.URL + "/dl"}).Fetch(ctx, "v1.0.10", "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "larger than") {
			t.Fatalf("an oversized archive: %v", err)
		}
	})
}

// TestInstallFailsSafely: when the new binary cannot be put in place, the
// old one stays where it was, whole, and nothing is left behind.
func TestInstallFailsSafely(t *testing.T) {
	t.Run("the directory is not writable", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("needs Unix permissions that bind this user")
		}
		dir := t.TempDir()
		target := filepath.Join(dir, "hive")
		if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		if err := update.Install(target, []byte("new")); err == nil || !strings.Contains(err.Error(), "permissions it was installed with") {
			t.Fatalf("install into a read-only directory: %v", err)
		}
		if data, _ := os.ReadFile(target); string(data) != "old" {
			t.Fatalf("target = %q, want the old binary", data)
		}
	})
	t.Run("the staged binary is gone", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "hive")
		if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
		staged, err := update.Stage(target, []byte("new"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(staged); err != nil {
			t.Fatal(err)
		}
		if err := update.Commit(staged, target); err == nil {
			t.Fatal("committed a binary that is not there")
		}
		if data, _ := os.ReadFile(target); string(data) != "old" {
			t.Fatalf("target = %q, want the old binary put back", data)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 1 {
			t.Fatalf("left behind: %v", entries)
		}
	})
}

// TestVerifyBinary: a binary is the release only when it is byte for byte
// the one in the signed release; a release that does not verify is an
// error, not a mismatch.
func TestVerifyBinary(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	ctx := context.Background()
	src := signedRelease(t, false, priv)
	src.PublicKey = pub
	v, err := src.VerifyBinary(ctx, update.ReleaseTag("0.8.0"), "linux", "arm64", []byte("#!new-hive"))
	if err != nil || !v.Identical || !v.Signed || v.Tag != "v0.8.0" || v.SHA256 != v.ReleaseSHA256 {
		t.Fatalf("the release binary: %+v, %v", v, err)
	}
	v, err = src.VerifyBinary(ctx, "v0.8.0", "linux", "arm64", []byte("#!built-elsewhere"))
	if err != nil || v.Identical || v.SHA256 == v.ReleaseSHA256 {
		t.Fatalf("another binary: %+v, %v", v, err)
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	forged := signedRelease(t, false, other)
	forged.PublicKey = pub
	if _, err := forged.VerifyBinary(ctx, "v0.8.0", "linux", "arm64", []byte("#!new-hive")); !errors.Is(err, update.ErrUnsigned) {
		t.Fatalf("a release signed by another key: %v", err)
	}
}
