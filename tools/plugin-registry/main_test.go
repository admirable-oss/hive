package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/plugin"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture is a registry directory with one official plugin of two
// releases, a publisher file with key (or none), and an approved community
// submission by acme.
func fixture(t *testing.T, officialKey string, acme plugin.PublicKey, acmeRelease map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "official-listings.json"), `{"auto-tab-title":{"id":"auto-tab-title","name":"Automatic tab titles","license":"MIT","maintainers":[{"name":"Admirable OSS"}],"repository":"https://github.com/admirable-oss/hive","plugin_api":"1","official":true}}`)
	write(t, filepath.Join(dir, "official-releases.json"), `{"schema":1,"plugins":[{"id":"auto-tab-title","subdir":"plugins/official/auto-tab-title","releases":[
		{"version":"0.1.0","ref":"v0.1.0","commit":"c8873a15f7a8c6bd8d743ad9afbba5ce33a7fc1a","checksum":"sha256:8f63b896ab674a078b97c23533f3c9a58e7fcb53071eaf63aaa5bec87afdb34b","published_at":"2026-10-14T22:32:14Z"},
		{"version":"0.2.0","ref":"v0.2.0","commit":"e831a293187599dd7ecf0b3bbdf8822da287b192","checksum":"sha256:9ce6a374e7d8ef1ff31d7bc0ba2b446974574403bdf34da8351f93ab3aa5381a","published_at":"2026-10-22T12:15:48Z"}]}]}`)
	keys := "[]"
	if officialKey != "" {
		keys = `[{"key":"` + officialKey + `"}]`
	}
	write(t, filepath.Join(dir, "publishers.json"), `{"schema":1,"publishers":[{"id":"hive","name":"Hive maintainers","tier":"official","keys":`+keys+`},{"id":"acme","name":"Acme","tier":"community","keys":[{"key":"`+acme.String()+`"}]}]}`)
	rel, _ := json.Marshal(acmeRelease)
	write(t, filepath.Join(dir, "submissions", "pr-status.json"), `{"schema":1,"plugin_id":"pr-status","state":"approved","listing":{"id":"pr-status","publisher":"acme","name":"PR status","license":"MIT","maintainers":[{"name":"Acme"}],"repository":"https://github.com/acme/pr-status","releases":[`+string(rel)+`]}}`)
	write(t, filepath.Join(dir, "submissions", "draft.json"), `{"schema":1,"plugin_id":"draft","state":"submitted","listing":{"id":"draft","publisher":"acme","name":"Draft","releases":[]}}`)
	return dir
}

func TestBuildSignAndRecord(t *testing.T) {
	officialPub, officialPriv, _ := ed25519.GenerateKey(rand.Reader)
	acmePub, acmePriv, _ := ed25519.GenerateKey(rand.Reader)
	acme := plugin.PublicKeyOf(acmePub)
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	r := plugin.Release{Version: "1.0.0", Ref: "v1.0.0", Commit: strings.Repeat("a", 40), Checksum: "sha256:" + strings.Repeat("b", 64), PluginAPI: 1, PublishedAt: at}
	acmeRelease := map[string]any{
		"version": r.Version, "ref": r.Ref, "commit": r.Commit, "checksum": r.Checksum, "plugin_api": 1, "published_at": at.Format(time.RFC3339),
		"signature": plugin.SignStatement(acmePriv, plugin.ReleaseStatement("pr-status", r)),
	}
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	keyEnv := env(map[string]string{"HIVE_PLUGIN_SIGNING_KEY": base64.StdEncoding.EncodeToString(officialPriv)})

	// No official key yet: not ready. verify succeeds (nothing is wrong),
	// build --if-ready writes nothing, and sign refuses a key the registry
	// does not list.
	dir := fixture(t, "", acme, acmeRelease)
	out := filepath.Join(t.TempDir(), "registry-2.json")
	if err := run([]string{"verify", "--dir", dir}, env(nil)); err != nil {
		t.Fatalf("verify before signing: %v", err)
	}
	if err := run([]string{"build", "--dir", dir, "-o", out, "--if-ready"}, env(nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("build --if-ready wrote an unready catalog")
	}
	if err := run([]string{"build", "--dir", dir, "-o", out}, env(nil)); !errors.Is(err, errNotReady) {
		t.Fatalf("build without --if-ready: %v", err)
	}
	if err := run([]string{"sign", "--dir", dir}, keyEnv); err == nil || !strings.Contains(err.Error(), "not a key of") {
		t.Fatalf("sign with an unlisted key: %v", err)
	}

	// With the key listed: sign, then build a catalog Hive reads, every
	// release signed.
	dir = fixture(t, plugin.PublicKeyOf(officialPub).String(), acme, acmeRelease)
	if err := run([]string{"sign", "--dir", dir}, keyEnv); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "official-signatures.json"))
	if err := run([]string{"sign", "--dir", dir}, keyEnv); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "official-signatures.json")); !bytes.Equal(after, before) {
		t.Fatal("signing again rewrote signatures")
	}
	results := filepath.Join(t.TempDir(), "results.json")
	write(t, results, `[{"plugin":"pr-status","version":"1.0.0","hive":"1.7.0-rc.1","result":"pass","at":"2026-11-02T00:00:00Z"},{"plugin":"auto-tab-title","version":"0.2.0","hive":"1.7.0-rc.1","result":"fail","at":"2026-11-02T00:00:00Z"}]`)
	if err := run([]string{"record-tests", "--dir", dir, results}, env(nil)); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"build", "--dir", dir, "-o", out}, env(nil)); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(out)
	catalog, err := plugin.ParseCatalog(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Plugins) != 2 {
		t.Fatalf("plugins: %+v (the unapproved submission is left out)", catalog.Plugins)
	}
	for _, p := range catalog.Plugins {
		for _, rel := range p.Releases {
			if rel.SignedBy == "" {
				t.Fatalf("%s %s unsigned", p.ID, rel.Version)
			}
		}
	}
	official, _ := catalog.Get("auto-tab-title")
	if official.Tier != plugin.TierOfficial || official.Publisher != "hive" || official.Releases[0].Tested[0].Result != "fail" {
		t.Fatalf("official listing: %+v", official)
	}
	community, _ := catalog.Get("pr-status")
	if community.Tier != plugin.TierCommunity || len(community.Releases[0].Tested) != 1 {
		t.Fatalf("community listing: %+v", community)
	}

	// A community release whose signature does not verify fails the build.
	acmeRelease["version"] = "1.0.1"
	acmeRelease["ref"] = "v1.0.1"
	dir = fixture(t, plugin.PublicKeyOf(officialPub).String(), acme, acmeRelease)
	if err := run([]string{"sign", "--dir", dir}, keyEnv); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "--dir", dir}, env(nil)); err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("a bad community signature: %v", err)
	}
}
