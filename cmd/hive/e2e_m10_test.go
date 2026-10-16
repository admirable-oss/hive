//go:build !windows

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

var m10VersionLine = regexp.MustCompile(`(?m)^version = "[^"]*"$`)

func m10CopyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func m10ArchiveChecksum(t *testing.T, repo, commit string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "archive", "--format=tar", commit).Output()
	if err != nil {
		t.Fatalf("git archive %s: %v", commit, err)
	}
	sum := sha256.Sum256(out)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func m10TagRelease(t *testing.T, repo, version string) (commit, checksum string) {
	t.Helper()
	manifestPath := filepath.Join(repo, "plugins", "pr-status", "hive-plugin.toml")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !m10VersionLine.Match(data) {
		t.Fatalf("no version line in %s:\n%s", manifestPath, data)
	}
	if err := os.WriteFile(manifestPath, m10VersionLine.ReplaceAll(data, []byte("version = \""+version+"\"")), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "release "+version)
	commit = strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))
	gitRun(t, repo, "tag", "v"+version)
	return commit, m10ArchiveChecksum(t, repo, commit)
}

func m10ReleaseRepo(t *testing.T) (repo, commit, checksum string) {
	t.Helper()
	repo = filepath.Join(t.TempDir(), "hive-plugins")
	pkg := filepath.Join(repo, "plugins", "pr-status")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	m10CopyTree(t, filepath.Join("..", "..", "examples", "plugins", "pr-status"), pkg)
	gitRun(t, repo, "init")
	commit, checksum = m10TagRelease(t, repo, "0.1.0")
	return repo, commit, checksum
}

func m10BumpRelease(t *testing.T, repo, version string) (commit, checksum string) {
	t.Helper()
	script := filepath.Join(repo, "plugins", "pr-status", "pr-status.sh")
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, append(append([]byte{}, data...), []byte("# "+version+"\n")...), 0o755); err != nil {
		t.Fatal(err)
	}
	return m10TagRelease(t, repo, version)
}

func m10ReleaseRecord(version, commit, checksum string) string {
	return `{"version":"` + version + `","ref":"v` + version + `","commit":"` + commit + `","checksum":"` + checksum + `","subdir":"plugins/pr-status","changelog":"Release ` + version + `.","published_at":"2026-10-14T00:00:00Z"}`
}

func m10Catalog(releases ...string) string {
	return `{"schema":1,"plugins":[{"id":"pr-status","name":"PR status","description":"Shows each environment's pull request and its checks as a sidebar token (needs gh).","installation":"Install with ` + "`hive plugin install pr-status`" + `.","categories":["developer-tools","notifications"],"maintainers":[{"name":"Example Maintainer","url":"https://example.org"}],"license":"MIT","repository":"https://example.invalid/hive-plugins.git","platforms":["darwin","linux"],"min_hive_version":"0.10.0","plugin_api":"1","releases":[` + strings.Join(releases, ",") + `]}]}`
}

func TestM10CatalogBehaviorMatrix(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	c := newCLI(t)
	repo, commit1, checksum1 := m10ReleaseRepo(t)
	commit2, checksum2 := m10BumpRelease(t, repo, "0.2.0")
	bareRepo := repo + ".git"
	if out, err := exec.Command("git", "clone", "--bare", repo, bareRepo).CombinedOutput(); err != nil {
		t.Fatalf("git clone --bare: %v\n%s", err, out)
	}
	gitcfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gitcfg, []byte("[url \""+bareRepo+"\"]\n\tinsteadOf = https://example.invalid/hive-plugins.git\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cloneDir := filepath.Join(t.TempDir(), "clone")
	clone := exec.Command("git", "clone", "--quiet", "--depth", "1", "--branch", "v0.1.0", "--", "https://example.invalid/hive-plugins.git", cloneDir)
	clone.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+gitcfg, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("fixture clone: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(cloneDir, "plugins", "pr-status")); err != nil {
		t.Fatalf("fixture clone is missing package: %v", err)
	}
	listing := m10Catalog(m10ReleaseRecord("0.1.0", commit1, checksum1))
	var listingMu sync.RWMutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		listingMu.RLock()
		body := listing
		listingMu.RUnlock()
		_, _ = w.Write([]byte(body))
	}))
	registryDown := false
	t.Cleanup(func() {
		if !registryDown {
			srv.Close()
		}
	})
	c.env = append(c.env, "HIVE_PLUGIN_REGISTRY_URL="+srv.URL, "GIT_CONFIG_GLOBAL="+gitcfg, "GIT_CONFIG_NOSYSTEM=1")

	if out := c.must("plugin", "search", "pull request"); !strings.Contains(out, "pr-status") || !strings.Contains(out, "0.1.0") {
		t.Fatalf("search:\n%s", out)
	}
	if out := c.must("plugin", "info", "pr-status"); !strings.Contains(out, "latest: 0.1.0") || !strings.Contains(out, "https://example.invalid/hive-plugins.git") {
		t.Fatalf("catalog info:\n%s", out)
	}
	if _, errOut, code := c.run("plugin", "install", "pr-status", "--ref", "v0.1.0"); code == 0 || !strings.Contains(errOut, "--ref applies to Git installs") {
		t.Fatalf("install with --ref: exit %d\n%s", code, errOut)
	}
	if out, errOut, code := c.run("plugin", "install", "pr-status"); code == 0 || !strings.Contains(errOut, "./pr-status.sh watch") || !strings.Contains(errOut, "--yes") {
		t.Fatalf("install without yes: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if out := c.must("plugin", "list"); !strings.Contains(out, "no plugins") {
		t.Fatalf("declined install left plugins:\n%s", out)
	}
	if out, errOut, code := c.run("plugin", "install", "pr-status", "--yes"); code != 0 || !strings.Contains(out, "installed plugin pr-status") || !strings.Contains(errOut, "Fetching catalog plugin pr-status@0.1.0") || !strings.Contains(errOut, "pinned commit: "+commit1) {
		t.Fatalf("catalog install: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	var rows []pluginRow
	decode(t, c.must("--json", "plugin", "list"), &rows)
	if len(rows) != 1 || rows[0].ID != "pr-status" || !rows[0].Registry || rows[0].Version != "0.1.0" || rows[0].Commit != commit1 || rows[0].Checksum != checksum1 {
		t.Fatalf("installed from catalog: %+v", rows)
	}
	script := filepath.Join(rows[0].Dir, "pr-status.sh")
	installed, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := c.run("plugin", "install", "pr-status", "--yes"); code == 0 || !strings.Contains(errOut, "already installed") {
		t.Fatalf("reinstall: exit %d\n%s", code, errOut)
	}
	listingMu.Lock()
	listing = m10Catalog(m10ReleaseRecord("0.1.0", commit1, checksum1), m10ReleaseRecord("0.2.0", commit2, "sha256:"+strings.Repeat("0", 64)))
	listingMu.Unlock()
	if _, errOut, code := c.run("plugin", "update", "pr-status", "--yes"); code == 0 || !strings.Contains(errOut, "checksum mismatch") {
		t.Fatalf("wrong checksum update: exit %d\n%s", code, errOut)
	}
	if got, err := os.ReadFile(script); err != nil || !bytes.Equal(installed, got) {
		t.Fatalf("failed update changed active plugin: %q (%v)", got, err)
	}
	listingMu.Lock()
	listing = m10Catalog(m10ReleaseRecord("0.1.0", commit1, checksum1), m10ReleaseRecord("0.2.0", commit2, checksum1))
	listingMu.Unlock()
	gitRun(t, repo, "tag", "-f", "v0.2.0", commit1)
	gitRun(t, "", "--git-dir="+bareRepo, "fetch", "--force", repo, "refs/tags/v0.2.0:refs/tags/v0.2.0")
	if _, errOut, code := c.run("plugin", "update", "pr-status", "--yes"); code == 0 || !strings.Contains(errOut, "does not match its manifest or pinned source revision") {
		t.Fatalf("update with a moved release tag: exit %d\n%s", code, errOut)
	}
	gitRun(t, repo, "tag", "-f", "v0.2.0", commit2)
	gitRun(t, "", "--git-dir="+bareRepo, "fetch", "--force", repo, "refs/tags/v0.2.0:refs/tags/v0.2.0")
	listingMu.Lock()
	listing = m10Catalog(m10ReleaseRecord("0.1.0", commit1, checksum1), m10ReleaseRecord("0.2.0", commit2, checksum2))
	listingMu.Unlock()
	if out, errOut, code := c.run("plugin", "update", "pr-status", "--yes"); code != 0 || !strings.Contains(out, "updated 1 plugin") || !strings.Contains(errOut, "Update pr-status 0.1.0 → 0.2.0") {
		t.Fatalf("update: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if out := c.must("plugin", "rollback", "pr-status"); !strings.Contains(out, "rolled back pr-status to 0.1.0") {
		t.Fatalf("rollback: %s", out)
	}
	if got, err := os.ReadFile(script); err != nil || !bytes.Equal(installed, got) {
		t.Fatalf("rollback failed to restore release: %q (%v)", got, err)
	}
	srv.Close()
	registryDown = true
	if _, errOut, code := c.run("plugin", "search"); code == 0 || !strings.Contains(errOut, "plugin registry unavailable") {
		t.Fatalf("search outage: exit %d\n%s", code, errOut)
	}
	if _, errOut, code := c.run("plugin", "install", "pr-status", "--yes"); code == 0 || !strings.Contains(errOut, "Git installation remains available") {
		t.Fatalf("install outage: exit %d\n%s", code, errOut)
	}
	if _, errOut, code := c.run("plugin", "update", "pr-status", "--yes"); code == 0 || !strings.Contains(errOut, "cannot update plugins while the registry is unavailable") {
		t.Fatalf("update outage: exit %d\n%s", code, errOut)
	}
	if out := c.must("plugin", "list"); !strings.Contains(out, "pr-status") || !strings.Contains(out, "0.1.0") {
		t.Fatalf("installed plugins during outage:\n%s", out)
	}
	linkedDir := pluginRepo(t, "spawn-reviewer")
	manifestPath := filepath.Join(linkedDir, "hive-plugin.toml")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest = bytes.Replace(manifest, []byte(`id = "spawn-reviewer"`), []byte(`id = "offline-link"`), 1)
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if out := c.must("plugin", "link", linkedDir); !strings.Contains(out, "linked plugin offline-link") {
		t.Fatalf("local link during outage:\n%s", out)
	}
	if out := c.must("plugin", "install", pluginRepo(t, "spawn-reviewer"), "--yes"); !strings.Contains(out, "installed plugin spawn-reviewer") {
		t.Fatalf("git install during outage:\n%s", out)
	}
}
