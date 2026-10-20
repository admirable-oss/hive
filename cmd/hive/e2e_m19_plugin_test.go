//go:build !windows

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func m19Revocations(versions ...string) string {
	recs := make([]string, 0, len(versions))
	for _, v := range versions {
		recs = append(recs, `{"id":"pr-status","version":"`+v+`","reason":"leaks the GitHub token","advisory":"https://example.org/advisories/1","revoked_at":"2026-10-18T00:00:00Z"}`)
	}
	return `{"schema":1,"releases":[` + strings.Join(recs, ",") + `]}`
}

// TestM19RevokedPluginReleases: a release the registry revokes is never
// installed or updated to, an installed one is reported by the doctor and
// left by `plugin update`, and a registry whose revocations cannot be read
// installs nothing.
func TestM19RevokedPluginReleases(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	c := newCLI(t)
	repo, commit1, checksum1 := m10ReleaseRepo(t)
	commit2, checksum2 := m10BumpRelease(t, repo, "0.2.0")
	commit3, checksum3 := m10BumpRelease(t, repo, "0.3.0")
	bareRepo := repo + ".git"
	if out, err := exec.Command("git", "clone", "--bare", repo, bareRepo).CombinedOutput(); err != nil {
		t.Fatalf("git clone --bare: %v\n%s", err, out)
	}
	gitcfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gitcfg, []byte("[url \""+bareRepo+"\"]\n\tinsteadOf = https://example.invalid/hive-plugins.git\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r1 := m10ReleaseRecord("0.1.0", commit1, checksum1)
	r2 := m10ReleaseRecord("0.2.0", commit2, checksum2)
	r3 := m10ReleaseRecord("0.3.0", commit3, checksum3)

	var mu sync.Mutex
	catalog, revocations, revocationStatus := m10Catalog(r1, r2), m19Revocations("0.2.0"), http.StatusOK
	set := func(cat, rev string, status int) {
		mu.Lock()
		defer mu.Unlock()
		catalog, revocations, revocationStatus = cat, rev, status
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/plugins/registry.json":
			_, _ = w.Write([]byte(catalog))
		case "/plugins/revocations.json":
			w.WriteHeader(revocationStatus)
			_, _ = w.Write([]byte(revocations))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c.env = append(c.env, "HIVE_PLUGIN_REGISTRY_URL="+srv.URL+"/plugins/registry.json", "GIT_CONFIG_GLOBAL="+gitcfg, "GIT_CONFIG_NOSYSTEM=1")

	// The newest release is revoked: search shows, and install takes, the
	// one before it; info says why.
	if out := c.must("plugin", "search", "pull request"); !strings.Contains(out, "0.1.0") || strings.Contains(out, "0.2.0") {
		t.Fatalf("search with 0.2.0 revoked:\n%s", out)
	}
	if out := c.must("plugin", "info", "pr-status"); !strings.Contains(out, "latest: 0.1.0") || !strings.Contains(out, "revoked: leaks the GitHub token; see https://example.org/advisories/1") {
		t.Fatalf("info with 0.2.0 revoked:\n%s", out)
	}
	if out, errOut, code := c.run("plugin", "install", "pr-status", "--yes"); code != 0 || !strings.Contains(errOut, "pr-status@0.1.0") {
		t.Fatalf("install with the newest release revoked: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if out := c.must("plugin", "doctor", "pr-status"); !strings.Contains(out, "registry: listed; 0.1.0 is current") {
		t.Fatalf("doctor with nothing installed revoked:\n%s", out)
	}

	// The installed release is revoked too, and a fixed one published: the
	// doctor fails, and update moves to the fixed release.
	set(m10Catalog(r1, r2, r3), m19Revocations("0.1.0", "0.2.0"), http.StatusOK)
	if out, errOut, code := c.run("plugin", "doctor", "pr-status"); code == 0 || !strings.Contains(out, "0.1.0 is revoked: leaks the GitHub token") {
		t.Fatalf("doctor with the installed release revoked: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if out, errOut, code := c.run("plugin", "update", "pr-status", "--yes"); code != 0 || !strings.Contains(errOut, "the installed pr-status 0.1.0 is revoked") || !strings.Contains(errOut, "Update pr-status 0.1.0 → 0.3.0") {
		t.Fatalf("update off a revoked release: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	var rows []pluginRow
	decode(t, c.must("--json", "plugin", "list"), &rows)
	if len(rows) != 1 || rows[0].Version != "0.3.0" || rows[0].Commit != commit3 {
		t.Fatalf("after the update: %+v", rows)
	}

	// The listing is archived: the doctor warns, nothing fails.
	set(strings.Replace(m10Catalog(r1, r2, r3), `"plugin_api":"1"`, `"plugin_api":"1","archived":true`, 1), m19Revocations("0.1.0", "0.2.0"), http.StatusOK)
	if out := c.must("plugin", "doctor", "pr-status"); !strings.Contains(out, "registry: archived") {
		t.Fatalf("doctor of an archived plugin:\n%s", out)
	}

	// Every release is revoked: there is nothing to update to.
	set(m10Catalog(r1, r2, r3), m19Revocations("0.1.0", "0.2.0", "0.3.0"), http.StatusOK)
	if _, errOut, code := c.run("plugin", "update", "pr-status", "--yes"); code == 0 || !strings.Contains(errOut, "every release of pr-status is revoked") {
		t.Fatalf("update with every release revoked: exit %d\n%s", code, errOut)
	}

	// Revocations that cannot be read stop installs, rather than risk one.
	set(m10Catalog(r1, r2, r3), "", http.StatusInternalServerError)
	if _, errOut, code := c.run("plugin", "search"); code == 0 || !strings.Contains(errOut, "cannot tell which plugin releases are revoked") {
		t.Fatalf("search without revocations: exit %d\n%s", code, errOut)
	}
	// A registry that publishes none (404) has revoked nothing.
	set(m10Catalog(r1, r2, r3), "", http.StatusNotFound)
	if out := c.must("plugin", "search"); !strings.Contains(out, "0.3.0") {
		t.Fatalf("search with no revocations document:\n%s", out)
	}
}
