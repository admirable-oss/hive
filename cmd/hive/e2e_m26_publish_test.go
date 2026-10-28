//go:build !windows

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/plugin"
)

// TestM26PublishSignedRelease: `hive plugin publish` tags, checksums and
// signs a release in a plugin's repository and pushes the tag; the record
// it writes, served in a schema 2 catalog, installs with its signature
// verified. A moved tag, uncommitted changes and an unsigned release
// without --unsigned are refused.
func TestM26PublishSignedRelease(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	pub := newM26Publisher(t)
	c := newCLI(t)
	repo, _, _ := m10ReleaseRepo(t)
	bare := repo + ".git"
	if out, err := exec.Command("git", "init", "--bare", "-q", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	gitRun(t, repo, "push", "-q", bare, "HEAD:refs/heads/main")
	pkg := filepath.Join(repo, "plugins", "pr-status")
	head, _ := m10TagRelease(t, repo, "0.4.0")
	gitRun(t, repo, "tag", "-d", "v0.4.0") // publish makes the tag

	// A dry run tags nothing.
	var rec plugin.ReleaseRecord
	decode(t, c.must("--json", "plugin", "publish", pkg, "--key", pub.keyPath, "--skip-tests", "--dry-run"), &rec)
	if rec.ID != "pr-status" || rec.Release.Ref != "pr-status/v0.4.0" || rec.Release.Subdir != "plugins/pr-status" || rec.Release.Commit != head || rec.Release.SignedBy != pub.key.Fingerprint() || rec.Release.Provenance == nil {
		t.Fatalf("dry-run record: %+v", rec)
	}
	if out := gitRun(t, repo, "tag", "--list", "pr-status/*"); strings.TrimSpace(out) != "" {
		t.Fatalf("a dry run tagged: %s", out)
	}

	// The real publish tags and pushes; its record is what the registry
	// lists, and the signature verifies against the publisher's key.
	recordFile := filepath.Join(t.TempDir(), "release.json")
	c.must("plugin", "publish", pkg, "--key", pub.keyPath, "--skip-tests", "--push", "--git-remote", bare, "--out", recordFile)
	data, err := os.ReadFile(recordFile)
	if err != nil {
		t.Fatal(err)
	}
	decode(t, string(data), &rec)
	if want := m10ArchiveChecksum(t, repo, head); rec.Release.Checksum != want {
		t.Fatalf("checksum %s, want %s", rec.Release.Checksum, want)
	}
	if _, err := plugin.VerifySignature(rec.Release.Signature, plugin.ReleaseStatement("pr-status", rec.Release), []plugin.PublicKey{pub.key}); err != nil {
		t.Fatalf("the published signature: %v", err)
	}
	if out := gitRun(t, bare, "rev-parse", "refs/tags/pr-status/v0.4.0"); strings.TrimSpace(out) != head {
		t.Fatalf("the pushed tag points at %s, not %s", out, head)
	}

	// Installing from a catalog listing that record checks its signature.
	entry, err := submissionEntry(rec.Release)
	if err != nil {
		t.Fatal(err)
	}
	catalog := pub.catalog(plugin.TierVerified, entry)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/plugins/registry-2.json" {
			_, _ = w.Write([]byte(catalog))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	gitcfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gitcfg, []byte("[url \""+bare+"\"]\n\tinsteadOf = https://example.invalid/hive-plugins.git\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.env = append(c.env, "HIVE_PLUGIN_REGISTRY_URL="+srv.URL+"/plugins/registry.json", "GIT_CONFIG_GLOBAL="+gitcfg, "GIT_CONFIG_NOSYSTEM=1")
	if out, errOut, code := c.run("plugin", "install", "pr-status", "--yes"); code != 0 || !strings.Contains(errOut, "signed by: "+pub.key.Fingerprint()) {
		t.Fatalf("install the published release: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}

	// The same version at another commit: the tag would move.
	if err := os.WriteFile(filepath.Join(pkg, "NOTES"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := c.run("plugin", "publish", pkg, "--key", pub.keyPath, "--skip-tests", "--dry-run"); code == 0 || !strings.Contains(errOut, "uncommitted changes") {
		t.Fatalf("publish a dirty tree: exit %d\n%s", code, errOut)
	}
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "notes")
	if _, errOut, code := c.run("plugin", "publish", pkg, "--key", pub.keyPath, "--skip-tests"); code == 0 || !strings.Contains(errOut, "already points at") {
		t.Fatalf("publish over a tag: exit %d\n%s", code, errOut)
	}
	// A tag that does not name the version is refused.
	if _, errOut, code := c.run("plugin", "publish", pkg, "--skip-tests", "--dry-run", "--ref", "pr-status/v0.4.0-x"); code == 0 || !strings.Contains(errOut, "does not name version") {
		t.Fatalf("a ref that does not name the version: exit %d\n%s", code, errOut)
	}
}

// m26TestPlugin writes a plugin with a [test] command into a directory.
func m26TestPlugin(t *testing.T, minHive, test string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "selftest")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `api = 1
id = "selftest"
version = "0.1.0"
min_hive_version = "` + minHive + `"

[permissions]
required = ["hive.read"]

[test]
command = ["sh", "-c", ` + "'" + test + "'" + `]
timeout = "2m"

[[actions]]
id = "hello"
command = ["true"]
`
	if err := os.WriteFile(filepath.Join(dir, plugin.ManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestM26PluginTest: `hive plugin test` validates, installs and checks a
// plugin in a scratch HIVE_HOME and runs its [test] command against the
// Hive under test; a failing test fails it.
func TestM26PluginTest(t *testing.T) {
	c := newCLI(t)
	good := m26TestPlugin(t, "1.7.0", `"$HIVE_BIN" plugin list | grep -q selftest && test "$HIVE_PLUGIN_TEST" = 1`)
	var res pluginTestResult
	out, errOut, code := c.run("--json", "plugin", "test", good)
	if code != 0 {
		t.Fatalf("plugin test: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	decode(t, out, &res)
	var names []string
	for _, ch := range res.Checks {
		names = append(names, ch.Name)
		if !ch.OK {
			t.Fatalf("check %s failed: %s", ch.Name, ch.Detail)
		}
	}
	if res.Result != "pass" || strings.Join(names, ",") != "scratch home,validate,install,doctor,test" {
		t.Fatalf("result: %+v", res)
	}
	// The scratch home is the test's own: nothing was installed here.
	if out := c.must("plugin", "list"); strings.Contains(out, "selftest") {
		t.Fatalf("plugin test installed into the real HIVE_HOME:\n%s", out)
	}

	bad := m26TestPlugin(t, "1.7.0", `echo broken; exit 3`)
	out, _, code = c.run("--json", "plugin", "test", bad)
	if code == 0 {
		t.Fatal("a failing [test] passed")
	}
	decode(t, out, &res)
	if res.Result != "fail" || !strings.Contains(res.failure(), "broken") {
		t.Fatalf("failing result: %+v", res)
	}

	// [test] is a key earlier Hive releases reject: publish needs the
	// manifest to require 1.7.0.
	old := m26TestPlugin(t, "1.0.2", "true")
	if _, errOut, code := c.run("plugin", "publish", old, "--unsigned", "--skip-tests", "--dry-run"); code == 0 || !strings.Contains(errOut, "needs min_hive_version") {
		t.Fatalf("publish [test] for older Hive: exit %d\n%s", code, errOut)
	}
}
