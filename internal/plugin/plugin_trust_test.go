package plugin

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testSign makes an armored SSHSIG signature of data, as `ssh-keygen -Y
// sign -n namespace` does with an ed25519 key.
func testSign(priv ed25519.PrivateKey, namespace string, data []byte) string {
	digest := sha512.Sum512(data)
	var signed bytes.Buffer
	signed.WriteString(sshsigMagic)
	sshString(&signed, []byte(namespace))
	sshString(&signed, nil)
	sshString(&signed, []byte("sha512"))
	sshString(&signed, digest[:])
	var sig bytes.Buffer
	sshString(&sig, []byte(sshEd25519))
	sshString(&sig, ed25519.Sign(priv, signed.Bytes()))
	var blob bytes.Buffer
	blob.WriteString(sshsigMagic)
	_ = binary.Write(&blob, binary.BigEndian, uint32(1))
	sshString(&blob, PublicKey{key: priv.Public().(ed25519.PublicKey)}.wire())
	sshString(&blob, []byte(namespace))
	sshString(&blob, nil)
	sshString(&blob, []byte("sha512"))
	sshString(&blob, sig.Bytes())
	return sshsigArmorIn + "\n" + base64.StdEncoding.EncodeToString(blob.Bytes()) + "\n" + sshsigArmorEn + "\n"
}

func testKey(t *testing.T) (PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return PublicKey{key: pub}, priv
}

func TestVerifySignature(t *testing.T) {
	key, priv := testKey(t)
	other, otherPriv := testKey(t)
	msg := []byte(`{"id":"x"}`)
	sig := testSign(priv, SignatureNamespace, msg)
	if got, err := VerifySignature(sig, msg, []PublicKey{other, key}); err != nil || got.Fingerprint() != key.Fingerprint() {
		t.Fatalf("a good signature: %v %v", got, err)
	}
	for name, c := range map[string]struct {
		sig  string
		msg  []byte
		keys []PublicKey
	}{
		"altered message":   {sig, []byte(`{"id":"y"}`), []PublicKey{key}},
		"another namespace": {testSign(priv, "git", msg), msg, []PublicKey{key}},
		"not the publisher": {testSign(otherPriv, SignatureNamespace, msg), msg, []PublicKey{key}},
		"garbage":           {"-----BEGIN SSH SIGNATURE-----\nAAAA\n-----END SSH SIGNATURE-----", msg, []PublicKey{key}},
		"not armored":       {"hello", msg, []PublicKey{key}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifySignature(c.sig, c.msg, c.keys); !errors.Is(err, ErrBadSignature) {
				t.Fatalf("verified: %v", err)
			}
		})
	}
	parsed, err := ParsePublicKey(key.String() + " me@example.org")
	if err != nil || parsed.Fingerprint() != key.Fingerprint() {
		t.Fatalf("ParsePublicKey round trip: %v", err)
	}
	if _, err := ParsePublicKey("ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ"); err == nil {
		t.Fatal("an RSA key is not a publisher key")
	}
}

// TestVerifySignatureFromSSHKeygen: a signature made by OpenSSH itself, as
// `hive plugin publish` makes them, verifies, and the fingerprint is the
// one ssh-keygen prints.
func TestVerifySignatureFromSSHKeygen(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen not installed")
	}
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519")
	if out, err := exec.Command(keygen, "-q", "-t", "ed25519", "-N", "", "-C", "test", "-f", keyPath).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pubLine, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	key, err := ParsePublicKey(string(pubLine))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(keygen, "-l", "-f", keyPath+".pub").Output()
	if err != nil || !strings.Contains(string(out), key.Fingerprint()) {
		t.Fatalf("fingerprint %s, ssh-keygen says %s (%v)", key.Fingerprint(), out, err)
	}
	msg := ReleaseStatement("pr-status", Release{Version: "1.0.0", Ref: "v1.0.0", Commit: strings.Repeat("a", 40), Checksum: "sha256:" + strings.Repeat("b", 64), PluginAPI: 1, PublishedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)})
	cmd := exec.Command(keygen, "-Y", "sign", "-n", SignatureNamespace, "-f", keyPath)
	cmd.Stdin = bytes.NewReader(msg)
	sig, err := cmd.Output()
	if err != nil {
		t.Fatalf("ssh-keygen -Y sign: %v", err)
	}
	if _, err := VerifySignature(string(sig), msg, []PublicKey{key}); err != nil {
		t.Fatalf("an OpenSSH signature: %v", err)
	}
}

func TestReleaseStatementIsCanonical(t *testing.T) {
	r := Release{Version: "1.0.0", Ref: "pr-status/v1.0.0", Commit: strings.Repeat("A", 40), Checksum: "SHA256:" + strings.Repeat("b", 64), PluginAPI: 2, PublishedAt: time.Date(2026, 11, 1, 2, 3, 4, 0, time.FixedZone("x", 3600))}
	got := string(ReleaseStatement("pr-status", r))
	want := `{"checksum":"sha256:` + strings.Repeat("b", 64) + `","commit":"` + strings.Repeat("a", 40) + `","id":"pr-status","plugin_api":2,"published_at":"2026-11-01T01:03:04Z","ref":"pr-status/v1.0.0","version":"1.0.0"}`
	if got != want {
		t.Fatalf("statement:\n got %s\nwant %s", got, want)
	}
}

// schema2Registry serves a schema 2 catalog (and optionally revocations);
// a path it does not know is 404.
func schema2Registry(t *testing.T, docs map[string]string) *RegistryClient {
	t.Helper()
	c := NewRegistryClient("https://registry.example/plugins/registry.json")
	c.HTTP = &http.Client{Transport: registryRoundTripper(func(r *http.Request) (*http.Response, error) {
		body, ok := docs[r.URL.Path]
		status := http.StatusOK
		if !ok {
			status = http.StatusNotFound
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	return c
}

type testRelease struct {
	version string
	signer  ed25519.PrivateKey
	tamper  bool
	api     int
	minHive string
	at      time.Time
}

func schema2Doc(t *testing.T, tier string, keys []PublicKey, releases ...testRelease) string {
	t.Helper()
	var rs []map[string]any
	for _, tr := range releases {
		at := tr.at
		if at.IsZero() {
			at = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
		}
		r := Release{Version: tr.version, Ref: "pr-status/v" + tr.version, Commit: strings.Repeat("a", 39) + tr.version[:1], Checksum: "sha256:" + strings.Repeat("b", 64), PluginAPI: tr.api, PublishedAt: at}
		m := map[string]any{
			"version": r.Version, "ref": r.Ref, "commit": r.Commit, "checksum": r.Checksum, "published_at": at.Format(time.RFC3339), "plugin_api": tr.api,
			"provenance": map[string]any{"builder": "github-actions", "workflow": ".github/workflows/release.yml", "run": "https://github.com/acme/pr-status/actions/runs/1"},
			"tested":     []map[string]any{{"hive": "1.7.0", "result": "pass", "at": at.Format(time.RFC3339)}},
			// A registry cannot claim a signer: Fetch decides it.
			"signed_by": "SHA256:forged",
		}
		if tr.minHive != "" {
			m["min_hive_version"] = tr.minHive
		}
		if tr.signer != nil {
			statement := ReleaseStatement("pr-status", r)
			if tr.tamper {
				statement = append(statement, ' ')
			}
			m["signature"] = testSign(tr.signer, SignatureNamespace, statement)
		}
		rs = append(rs, m)
	}
	var ks []map[string]any
	for _, k := range keys {
		ks = append(ks, map[string]any{"key": k.String(), "fingerprint": k.Fingerprint()})
	}
	doc := map[string]any{
		"schema":     2,
		"publishers": []map[string]any{{"id": "acme", "name": "Acme Inc.", "tier": tier, "keys": ks}},
		"plugins": []map[string]any{{
			"id": "pr-status", "publisher": "acme", "name": "PR Status", "repository": "https://github.com/acme/pr-status", "license": "MIT",
			"maintainers": []map[string]any{{"name": "Acme"}}, "releases": rs,
		}},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRegistrySchema2(t *testing.T) {
	ctx := context.Background()
	key, priv := testKey(t)
	_, stranger := testKey(t)
	doc := schema2Doc(t, TierVerified, []PublicKey{key},
		testRelease{version: "1.0.0", signer: priv, api: 1},
		testRelease{version: "2.0.0", signer: priv, tamper: true, api: 1},
		testRelease{version: "3.0.0", signer: stranger, api: 1},
		testRelease{version: "4.0.0", api: 1},
		testRelease{version: "5.0.0", signer: priv, api: 99},
	)
	catalog, err := schema2Registry(t, map[string]string{"/plugins/registry-2.json": doc}).Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Schema != 2 {
		t.Fatalf("schema %d", catalog.Schema)
	}
	p, _ := catalog.Get("pr-status")
	if p.Tier != TierVerified || !p.Verified || p.Official || p.Publisher != "acme" {
		t.Fatalf("listing trust: %+v", p)
	}
	byVersion := map[string]Release{}
	for _, r := range p.Releases {
		byVersion[r.Version] = r
	}
	if r := byVersion["1.0.0"]; r.SignedBy != key.Fingerprint() || r.SignatureError != "" || r.Provenance == nil || len(r.Tested) != 1 {
		t.Fatalf("a signed release: %+v", r)
	}
	for _, v := range []string{"2.0.0", "3.0.0"} {
		if r := byVersion[v]; r.SignedBy != "" || r.SignatureError == "" {
			t.Fatalf("release %s with a bad signature: %+v", v, r)
		}
	}
	if r := byVersion["4.0.0"]; r.SignedBy != "" || r.SignatureError != "" {
		t.Fatalf("an unsigned release: %+v", r)
	}
	// The newest release this hive can run: 5.0.0 needs plugin API 99.
	if r, ok := p.LatestFor("1.7.0"); !ok || r.Version != "4.0.0" {
		t.Fatalf("LatestFor = %v %v", r.Version, ok)
	}

	// The policy: verified plugins must be signed, and a bad signature
	// never installs unless nothing is checked.
	policy := func(policy, version string) error {
		_, err := CheckSignature(policy, catalog.Schema, p, byVersion[version])
		return err
	}
	if policy(SignaturesOfficialVerified, "1.0.0") != nil || policy(SignaturesAll, "1.0.0") != nil {
		t.Fatal("a signed release installs")
	}
	if err := policy(SignaturesOfficialVerified, "2.0.0"); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("a tampered release: %v", err)
	}
	if err := policy(SignaturesOfficialVerified, "4.0.0"); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("an unsigned verified release: %v", err)
	}
	if policy(SignaturesNone, "2.0.0") != nil {
		t.Fatal("none checks nothing")
	}
	community := p
	community.Tier = TierCommunity
	if w, err := CheckSignature(SignaturesOfficialVerified, 2, community, byVersion["4.0.0"]); err != nil || w == "" {
		t.Fatalf("an unsigned community release installs with a warning: %q %v", w, err)
	}
	if _, err := CheckSignature(SignaturesAll, 2, community, byVersion["4.0.0"]); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("all: %v", err)
	}
	// A schema 1 registry has no signatures: a warning by default, refused
	// under all.
	if w, err := CheckSignature(SignaturesOfficialVerified, 1, p, Release{Version: "1.0.0"}); err != nil || !strings.Contains(w, "schema 1") {
		t.Fatalf("schema 1: %q %v", w, err)
	}
	if _, err := CheckSignature(SignaturesAll, 1, p, Release{Version: "1.0.0"}); err == nil {
		t.Fatal("all refuses a schema 1 release")
	}

	// Continuity: an update keeps the publisher, and a signed install is
	// not followed by an unsigned release.
	installed := Installed{ID: "pr-status", Version: "1.0.0", Trust: Trust{Publisher: "acme", SignedBy: key.Fingerprint()}}
	if err := CheckContinuity(SignaturesOfficialVerified, installed, p, byVersion["4.0.0"]); err == nil {
		t.Fatal("signed then unsigned")
	}
	moved := p
	moved.Publisher = "mallory"
	if err := CheckContinuity(SignaturesOfficialVerified, installed, moved, byVersion["1.0.0"]); err == nil {
		t.Fatal("a listing moved to another publisher")
	}
	if err := CheckContinuity(SignaturesOfficialVerified, installed, p, byVersion["1.0.0"]); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrySchema2KeyRevocation(t *testing.T) {
	key, priv := testKey(t)
	early := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	doc := schema2Doc(t, TierCommunity, []PublicKey{key},
		testRelease{version: "1.0.0", signer: priv, at: early},
		testRelease{version: "2.0.0", signer: priv, at: late},
	)
	revocations := `{"schema":1,"releases":[],"keys":[{"fingerprint":"` + key.Fingerprint() + `","reason":"the key leaked","since":"2026-11-15T00:00:00Z"}]}`
	catalog, err := schema2Registry(t, map[string]string{"/plugins/registry-2.json": doc, "/plugins/revocations.json": revocations}).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p, _ := catalog.Get("pr-status")
	if r, ok := p.LatestFor("1.7.0"); !ok || r.Version != "1.0.0" {
		t.Fatalf("a release signed after the key was distrusted is revoked: %v %v", r.Version, ok)
	}
	if rev, ok := catalog.Revocation("pr-status", "2.0.0"); !ok || !strings.Contains(rev.Reason, "the key leaked") {
		t.Fatalf("revocation: %+v %v", rev, ok)
	}
	if _, ok := catalog.Revocation("pr-status", "1.0.0"); ok {
		t.Fatal("a release signed before the key was distrusted stays")
	}
}

func TestRegistrySchema2Fallback(t *testing.T) {
	ctx := context.Background()
	// No schema 2 (404): the schema 1 catalog is read, with tiers from its
	// flags.
	official := strings.Replace(testRegistry, `"license":"MIT"`, `"license":"MIT","official":true`, 1)
	catalog, err := schema2Registry(t, map[string]string{"/plugins/registry.json": official}).Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := catalog.Get("pr-status"); catalog.Schema != 1 || p.Tier != TierOfficial {
		t.Fatalf("schema 1: %d %+v", catalog.Schema, p)
	}
	// A schema 2 catalog that fails otherwise is an error, never a reason
	// to read the one without signatures.
	c := NewRegistryClient("https://registry.example/plugins/registry.json")
	c.HTTP = &http.Client{Transport: registryRoundTripper(func(r *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, testRegistry
		if strings.HasSuffix(r.URL.Path, Schema2File) {
			status, body = http.StatusInternalServerError, ""
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if _, err := c.Fetch(ctx); err == nil {
		t.Fatal("a failing schema 2 catalog fell back to schema 1")
	}
	// A listing naming an unknown publisher, or a publisher with a bad key,
	// makes the catalog invalid.
	key, _ := testKey(t)
	doc := schema2Doc(t, TierCommunity, []PublicKey{key}, testRelease{version: "1.0.0"})
	for name, bad := range map[string]string{
		"unknown publisher": strings.Replace(doc, `"publisher":"acme"`, `"publisher":"nobody"`, 1),
		"wrong fingerprint": strings.Replace(doc, key.Fingerprint(), "SHA256:wrong", 1),
		"bad tier":          strings.Replace(doc, `"tier":"community"`, `"tier":"gold"`, 1),
		"bad ref":           strings.Replace(doc, `"ref":"pr-status/v1.0.0"`, `"ref":"pr-status/v9.0.0"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := schema2Registry(t, map[string]string{"/plugins/registry-2.json": bad}).Fetch(ctx); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestRefMatchesVersion(t *testing.T) {
	for _, c := range []struct {
		ref, version string
		perPlugin    bool
		want         bool
	}{
		{"v1.2.0", "1.2.0", false, true},
		{"1.2.0", "1.2.0", false, true},
		{"pr-status/v1.2.0", "1.2.0", false, false},
		{"pr-status/v1.2.0", "1.2.0", true, true},
		{"plugins/pr-status/1.2.0", "1.2.0", true, true},
		{"pr-status/v1.2.1", "1.2.0", true, false},
		{"/v1.2.0", "1.2.0", true, false},
		{"../v1.2.0", "1.2.0", true, false},
		{"", "1.2.0", true, false},
	} {
		if got := refMatchesVersion(c.ref, c.version, c.perPlugin); got != c.want {
			t.Errorf("refMatchesVersion(%q, %q, %v) = %v", c.ref, c.version, c.perPlugin, got)
		}
	}
}
