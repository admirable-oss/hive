package updater_test

import (
	"archive/zip"
	"bytes"
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
	"time"

	"github.com/admirable-oss/hive/desktop/internal/updater"
)

// publish serves a fake release tag whose manifest lists files and is
// signed by key (unsigned when key is nil; no manifest when files is nil).
func publish(t *testing.T, tag string, files map[string][]byte, key ed25519.PrivateKey, mutate func(name string, b []byte) []byte) (api, dl string) {
	t.Helper()
	var sums strings.Builder
	for name, b := range files {
		s := sha256.Sum256(b)
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(s[:]), name)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/releases/latest", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintf(w, `{"tag_name":%q}`, tag) })
	mux.HandleFunc("/api/releases", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `[{"tag_name":"v9.0.0","draft":true},{"tag_name":%q},{"tag_name":"v1.0.0"}]`, tag)
	})
	if files != nil { // nil: a release without desktop packages
		mux.HandleFunc("/dl/"+tag+"/"+updater.Manifest, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, sums.String()) })
	}
	if key != nil {
		mux.HandleFunc("/dl/"+tag+"/"+updater.Manifest+".sig", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(sums.String()))))
		})
	}
	for name, b := range files {
		body := b
		if mutate != nil {
			body = mutate(name, b)
		}
		mux.HandleFunc("/dl/"+tag+"/"+name, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/api", srv.URL + "/dl"
}

func TestCheckAndDownload(t *testing.T) {
	ctx := context.Background()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	asset := updater.Asset("v1.2.0", "linux", "amd64")
	files := map[string][]byte{asset: []byte("new-appimage")}
	cfg := func(api, dl string) updater.Config {
		return updater.Config{Version: "1.1.0", PublicKey: pub, GOOS: "linux", GOARCH: "amd64", APIURL: api, DownloadURL: dl}
	}

	t.Run("a newer signed release downloads", func(t *testing.T) {
		c := cfg(publish(t, "v1.2.0", files, priv, nil))
		rel, err := c.Check(ctx)
		if err != nil || !rel.Newer || rel.Asset != asset || rel.Version != "1.2.0" {
			t.Fatalf("check = %+v, %v", rel, err)
		}
		path, err := c.Download(ctx, rel, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(path); string(b) != "new-appimage" {
			t.Fatalf("downloaded %q", b)
		}
	})
	t.Run("the same version is not newer", func(t *testing.T) {
		c := cfg(publish(t, "v1.1.0", map[string][]byte{updater.Asset("v1.1.0", "linux", "amd64"): []byte("x")}, priv, nil))
		if rel, err := c.Check(ctx); err != nil || rel.Newer {
			t.Fatalf("check = %+v, %v", rel, err)
		}
	})
	t.Run("a development build never updates", func(t *testing.T) {
		c := cfg(publish(t, "v1.2.0", files, priv, nil))
		c.Version = "dev"
		if rel, err := c.Check(ctx); err != nil || !rel.Newer {
			t.Fatalf("a release is newer than dev: %+v, %v", rel, err)
		}
		c.PublicKey = nil
		if _, err := c.Check(ctx); !errors.Is(err, updater.ErrNoKey) {
			t.Fatalf("no key: %v", err)
		}
	})
	t.Run("a release signed by another key is refused", func(t *testing.T) {
		c := cfg(publish(t, "v1.2.0", files, other, nil))
		if _, err := c.Check(ctx); !errors.Is(err, updater.ErrUnsigned) {
			t.Fatalf("forged: %v", err)
		}
	})
	t.Run("an unsigned release is refused", func(t *testing.T) {
		c := cfg(publish(t, "v1.2.0", files, nil, nil))
		if _, err := c.Check(ctx); !errors.Is(err, updater.ErrUnsigned) {
			t.Fatalf("unsigned: %v", err)
		}
	})
	t.Run("a tampered package is refused", func(t *testing.T) {
		c := cfg(publish(t, "v1.2.0", files, priv, func(_ string, b []byte) []byte { return append(b, '!') }))
		rel, err := c.Check(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Download(ctx, rel, t.TempDir()); err == nil || !strings.Contains(err.Error(), "checksum") {
			t.Fatalf("tampered: %v", err)
		}
	})
	t.Run("a release without a package for this system", func(t *testing.T) {
		c := cfg(publish(t, "v1.2.0", files, priv, nil))
		c.GOOS = "darwin"
		rel, err := c.Check(ctx)
		if err != nil || rel.Asset != "" {
			t.Fatalf("check = %+v, %v", rel, err)
		}
		if _, err := c.Download(ctx, rel, t.TempDir()); err == nil {
			t.Fatal("downloaded a package that does not exist")
		}
	})
	t.Run("a release without desktop packages is no update", func(t *testing.T) {
		c := cfg(publish(t, "v1.2.0", nil, priv, nil))
		rel, err := c.Check(ctx)
		if err != nil || rel.Asset != "" || !rel.Newer {
			t.Fatalf("check = %+v, %v", rel, err)
		}
	})
	t.Run("the preview channel takes release candidates", func(t *testing.T) {
		c := cfg(publish(t, "v1.3.0-rc.10", map[string][]byte{updater.Asset("v1.3.0-rc.10", "linux", "amd64"): []byte("rc")}, priv, nil))
		c.Channel = "preview"
		if rel, err := c.Check(ctx); err != nil || rel.Tag != "v1.3.0-rc.10" || !rel.Newer {
			t.Fatalf("preview = %+v, %v", rel, err)
		}
	})
}

func TestNewer(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want bool
	}{
		{"v1.2.0", "1.1.0", true},
		{"v1.1.0", "v1.1.0", false},
		{"v1.0.0-rc.10", "v1.0.0-rc.9", true},
		{"v1.0.0", "v1.0.0-rc.9", true},
		{"dev", "v1.0.0", false},
		{"v1.0.0", "dev", true},
	} {
		if got := updater.Newer(tt.a, tt.b); got != tt.want {
			t.Errorf("Newer(%q, %q) = %v", tt.a, tt.b, got)
		}
	}
}

func TestLocate(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if tg, err := updater.Locate("darwin", "/Applications/Hive.app/Contents/MacOS/hive-desktop", env(nil)); err != nil || tg.Kind != updater.KindApp || tg.Path != "/Applications/Hive.app" {
		t.Fatalf("a bundle: %+v, %v", tg, err)
	}
	if _, err := updater.Locate("darwin", "/Users/me/hive/desktop/bin/hive-desktop", env(nil)); !errors.Is(err, updater.ErrUnmanaged) {
		t.Fatalf("a bare binary: %v", err)
	}
	if _, err := updater.Locate("darwin", "/opt/homebrew/Caskroom/hive-desktop/1.1.0/Hive.app/Contents/MacOS/hive-desktop", env(nil)); !errors.Is(err, updater.ErrUnmanaged) {
		t.Fatalf("a cask: %v", err)
	}
	if tg, err := updater.Locate("linux", "/tmp/.mount_x/hive-desktop", env(map[string]string{"APPIMAGE": "/home/me/Hive.AppImage"})); err != nil || tg.Kind != updater.KindAppImage || tg.Path != "/home/me/Hive.AppImage" {
		t.Fatalf("an AppImage: %+v, %v", tg, err)
	}
	if _, err := updater.Locate("linux", "/usr/local/bin/hive-desktop", env(nil)); !errors.Is(err, updater.ErrUnmanaged) {
		t.Fatalf("a .deb: %v", err)
	}
	if tg, err := updater.Locate("windows", `C:\Users\me\AppData\Local\Programs\Hive\hive-desktop.exe`, env(nil)); err != nil || tg.Kind != updater.KindInstaller {
		t.Fatalf("Windows: %+v, %v", tg, err)
	}
}

// zipOf builds a zip; entries ending in "/" are directories, "->" makes a
// symlink.
func zipOf(t *testing.T, entries map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		switch {
		case strings.HasSuffix(name, "/"):
			h.SetMode(0o755 | os.ModeDir)
		case strings.HasPrefix(body, "->"):
			h.SetMode(0o777 | os.ModeSymlink)
			body = strings.TrimPrefix(body, "->")
		default:
			h.SetMode(0o755)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	path := filepath.Join(t.TempDir(), "pkg.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStageApp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bundles are macOS's; symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	tg := updater.Target{Kind: updater.KindApp, Path: filepath.Join(dir, "Hive.app"), Exe: filepath.Join(dir, "Hive.app", "Contents", "MacOS", "hive-desktop")}
	good := zipOf(t, map[string]string{
		"Hive.app/Contents/MacOS/hive-desktop": "#!new",
		"Hive.app/Contents/Resources/hive":     "#!cli",
		"Hive.app/Contents/Current":            "->Resources",
		// AppleDouble files, as a zip made on macOS has them.
		"Hive.app/Contents/MacOS/._hive-desktop": "attrs",
		"Hive.app/._Contents":                    "attrs",
		"__MACOSX/Hive.app/._Contents":           "attrs",
	})
	staged, err := updater.Stage(tg, good)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"Contents/MacOS/._hive-desktop", "._Contents", "../__MACOSX"} {
		if _, err := os.Lstat(filepath.Join(staged, f)); err == nil {
			t.Errorf("%s was unpacked: it would break the bundle's signature", f)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(staged, "Contents", "MacOS", "hive-desktop")); string(b) != "#!new" {
		t.Fatalf("staged %s: %q", staged, b)
	}
	if info, err := os.Stat(filepath.Join(staged, "Contents", "MacOS", "hive-desktop")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("the binary lost its executable bit: %v", info)
	}
	for name, entries := range map[string]map[string]string{
		"an entry outside the folder": {"../evil": "x", "Hive.app/Contents/MacOS/hive-desktop": "x"},
		"a symlink outside":           {"Hive.app/Contents/MacOS/hive-desktop": "x", "Hive.app/escape": "->../../../etc"},
		"no app":                      {"README": "x"},
		"an app without the binary":   {"Hive.app/Contents/Info.plist": "x"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := updater.Stage(tg, zipOf(t, entries)); err == nil {
				t.Fatal("staged a bad package")
			}
		})
	}
}

// fakeApp writes an executable script that, when an update launched it,
// reports to the supervisor if healthy, and otherwise exits at once; it
// leaves a mark when it starts.
func fakeApp(t *testing.T, path, name string, healthy bool, state string) {
	t.Helper()
	script := "#!/bin/sh\necho started > " + filepath.Join(state, name+".started") + "\n"
	if healthy {
		script += `[ -n "$` + updater.TokenEnv + `" ] && printf '%s\n' "$` + updater.TokenEnv + `" > ` + filepath.Join(state, "update-health") + "\nsleep 1\n"
	} else {
		script += "exit 1\n"
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func waitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}

func TestSupervise(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake apps are shell scripts")
	}
	run := func(t *testing.T, newHealthy bool) (updater.Result, string, string, error) {
		dir, state := t.TempDir(), t.TempDir()
		tg := updater.Target{Kind: updater.KindAppImage, Path: filepath.Join(dir, "Hive.AppImage")}
		tg.Exe = tg.Path
		fakeApp(t, tg.Path, "old", true, state)
		staged := tg.Path + ".staged"
		fakeApp(t, staged, "new", newHealthy, state)
		plan, err := updater.NewPlan(tg, staged, "1.1.0", "1.2.0", state)
		if err != nil {
			t.Fatal(err)
		}
		res, err := updater.Supervise(plan, updater.Options{Health: 3 * time.Second, ParentExit: time.Millisecond, Poll: 20 * time.Millisecond})
		return res, tg.Path, state, err
	}

	t.Run("a healthy update stays", func(t *testing.T) {
		res, path, state, err := run(t, true)
		if err != nil || !res.OK || res.To != "1.2.0" {
			t.Fatalf("supervise = %+v, %v", res, err)
		}
		if b, _ := os.ReadFile(path); !strings.Contains(string(b), "new.started") {
			t.Fatal("the new version is not installed")
		}
		if b, _ := os.ReadFile(path + ".old"); !strings.Contains(string(b), "old.started") {
			t.Fatal("the previous version is not kept")
		}
		if r, ok := updater.TakeResult(state); !ok || !r.OK {
			t.Fatalf("result = %+v, %v", r, ok)
		}
		if _, ok := updater.TakeResult(state); ok {
			t.Fatal("the result was reported twice")
		}
	})
	t.Run("an update that does not start is rolled back", func(t *testing.T) {
		res, path, state, err := run(t, false)
		if err == nil || res.OK || !strings.Contains(res.Reason, "did not start") {
			t.Fatalf("supervise = %+v, %v", res, err)
		}
		if b, _ := os.ReadFile(path); !strings.Contains(string(b), "old.started") {
			t.Fatal("the previous version was not put back")
		}
		waitFile(t, filepath.Join(state, "old.started")) // and started again
		if r, ok := updater.TakeResult(state); !ok || r.OK {
			t.Fatalf("result = %+v, %v", r, ok)
		}
	})
}

func TestReportStarted(t *testing.T) {
	state := t.TempDir()
	updater.ReportStarted(state) // no token: nothing
	if _, err := os.Stat(filepath.Join(state, "update-health")); err == nil {
		t.Fatal("reported without being asked")
	}
	t.Setenv(updater.TokenEnv, "abc")
	updater.ReportStarted(state)
	if b, _ := os.ReadFile(filepath.Join(state, "update-health")); strings.TrimSpace(string(b)) != "abc" {
		t.Fatalf("health = %q", b)
	}
	if os.Getenv(updater.TokenEnv) != "" {
		t.Fatal("the token stays in the environment")
	}
}
