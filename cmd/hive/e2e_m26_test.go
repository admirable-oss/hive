//go:build !windows

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/plugin"
)

// m26Publisher is an ed25519 SSH key made by ssh-keygen, as publishers
// sign with.
type m26Publisher struct {
	t       *testing.T
	keyPath string
	key     plugin.PublicKey
}

func newM26Publisher(t *testing.T) m26Publisher {
	t.Helper()
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen not installed")
	}
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if out, err := exec.Command(keygen, "-q", "-t", "ed25519", "-N", "", "-C", "acme", "-f", keyPath).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	line, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	key, err := plugin.ParsePublicKey(string(line))
	if err != nil {
		t.Fatal(err)
	}
	return m26Publisher{t: t, keyPath: keyPath, key: key}
}

// release is a schema 2 release record, signed when sign is set.
func (p m26Publisher) release(version, commit, checksum string, at time.Time, sign bool) map[string]any {
	p.t.Helper()
	r := plugin.Release{Version: version, Ref: "v" + version, Commit: commit, Checksum: checksum, PluginAPI: 1, PublishedAt: at}
	rec := map[string]any{
		"version": version, "ref": r.Ref, "commit": commit, "checksum": checksum, "subdir": "plugins/pr-status",
		"plugin_api": 1, "published_at": at.Format(time.RFC3339),
		"provenance": map[string]any{"builder": "github-actions", "workflow": ".github/workflows/release.yml", "run": "https://github.com/acme/pr-status/actions/runs/7"},
	}
	if sign {
		cmd := exec.Command("ssh-keygen", "-Y", "sign", "-n", plugin.SignatureNamespace, "-f", p.keyPath)
		cmd.Stdin = bytes.NewReader(plugin.ReleaseStatement("pr-status", r))
		sig, err := cmd.Output()
		if err != nil {
			p.t.Fatalf("ssh-keygen -Y sign: %v", err)
		}
		rec["signature"] = string(sig)
	}
	return rec
}

func (p m26Publisher) catalog(tier string, releases ...map[string]any) string {
	p.t.Helper()
	doc := map[string]any{
		"schema":     2,
		"publishers": []any{map[string]any{"id": "acme", "name": "Acme Inc.", "url": "https://acme.example", "tier": tier, "keys": []any{map[string]any{"key": p.key.String()}}}},
		"plugins": []any{map[string]any{
			"id": "pr-status", "publisher": "acme", "name": "PR status", "license": "MIT",
			"maintainers": []any{map[string]any{"name": "Acme"}}, "repository": "https://example.invalid/hive-plugins.git",
			"platforms": []string{"darwin", "linux"}, "plugin_api": "1", "releases": releases,
		}},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		p.t.Fatal(err)
	}
	return string(b)
}

// TestM26SignedPluginReleases: a schema 2 registry's signed release
// installs with its tier, publisher, signer and provenance recorded and
// shown; an unsigned release of a verified publisher is refused; and a
// revoked publisher key revokes what it signed from then on, which the
// doctor reports and every listing shows.
func TestM26SignedPluginReleases(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	pub := newM26Publisher(t)
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
	nov1, nov10, nov20 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	r1 := pub.release("0.1.0", commit1, checksum1, nov1, true)
	r2 := pub.release("0.2.0", commit2, checksum2, nov10, true)
	r3 := pub.release("0.3.0", commit3, checksum3, nov20, false)

	var mu sync.Mutex
	catalog, revocations := pub.catalog(plugin.TierVerified, r1, r2), `{"schema":1,"releases":[]}`
	set := func(cat, rev string) {
		mu.Lock()
		defer mu.Unlock()
		catalog, revocations = cat, rev
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/plugins/registry-2.json":
			_, _ = w.Write([]byte(catalog))
		case "/plugins/revocations.json":
			_, _ = w.Write([]byte(revocations))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c.env = append(c.env, "HIVE_PLUGIN_REGISTRY_URL="+srv.URL+"/plugins/registry.json", "GIT_CONFIG_GLOBAL="+gitcfg, "GIT_CONFIG_NOSYSTEM=1")
	fp := pub.key.Fingerprint()

	// The catalog shows who publishes it and who signed each release.
	if out := c.must("plugin", "info", "pr-status"); !strings.Contains(out, "publisher: acme") || !strings.Contains(out, "tier: verified (publisher identity reviewed)") || !strings.Contains(out, "signed by: "+fp) || !strings.Contains(out, "built by: github-actions") {
		t.Fatalf("catalog info:\n%s", out)
	}

	// Installing shows and records the release's trust.
	out, errOut, code := c.run("plugin", "install", "pr-status", "--yes")
	if code != 0 || !strings.Contains(errOut, "tier: verified") || !strings.Contains(errOut, "signed by: "+fp) {
		t.Fatalf("install: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	var rows []pluginRow
	decode(t, c.must("--json", "plugin", "list"), &rows)
	if len(rows) != 1 || rows[0].Version != "0.2.0" || rows[0].Tier != plugin.TierVerified || rows[0].Publisher != "acme" || rows[0].SignedBy != fp || rows[0].Provenance == nil || rows[0].Provenance.Builder != "github-actions" {
		t.Fatalf("installed: %+v", rows)
	}
	if out := c.must("plugin", "list"); !strings.Contains(out, "TIER") || !strings.Contains(out, "verified") {
		t.Fatalf("list:\n%s", out)
	}
	if out := c.must("plugin", "doctor", "pr-status"); !strings.Contains(out, "signature: 0.2.0 signed by "+fp) {
		t.Fatalf("doctor of a signed release:\n%s", out)
	}

	// An unsigned release of a verified publisher is refused.
	set(pub.catalog(plugin.TierVerified, r1, r2, r3), `{"schema":1,"releases":[]}`)
	if _, errOut, code := c.run("plugin", "update", "pr-status", "--yes"); code == 0 || !strings.Contains(errOut, "the release is not signed") {
		t.Fatalf("update to an unsigned release: exit %d\n%s", code, errOut)
	}

	// The key is revoked from 15 November: 0.2.0 (10 November) stands, and
	// so would anything signed before; a release it signs later does not.
	since := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	keyRevoked := `{"schema":1,"releases":[],"keys":[{"fingerprint":"` + fp + `","reason":"the key leaked","advisory":"https://acme.example/advisory","since":"` + since.Format(time.RFC3339) + `"}]}`
	set(pub.catalog(plugin.TierVerified, r1, r2), keyRevoked)
	if out, errOut, code := c.run("plugin", "doctor", "pr-status"); code == 0 || !strings.Contains(out, "0.2.0 is revoked: signed with a revoked publisher key") {
		t.Fatalf("doctor with the key revoked: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	decode(t, c.must("--json", "plugin", "list"), &rows)
	if len(rows) != 1 || rows[0].Revoked == nil || !strings.Contains(rows[0].Revoked.Reason, "the key leaked") {
		t.Fatalf("the doctor recorded the revocation: %+v", rows)
	}
	if out := c.must("plugin", "list"); !strings.Contains(out, "REVOKED") {
		t.Fatalf("list shows the revocation:\n%s", out)
	}
	if out := c.must("plugin", "info", "pr-status"); !strings.Contains(out, "revoked: signed with a revoked publisher key") {
		t.Fatalf("info shows the revocation:\n%s", out)
	}
}
