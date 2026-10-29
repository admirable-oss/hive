package plugin

import (
	"strings"
	"testing"
	"time"
)

func TestProvenanceFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if p := ProvenanceFromEnv(env(nil)); p.Builder != "local" || p.Repository != "" {
		t.Fatalf("outside CI: %+v", p)
	}
	p := ProvenanceFromEnv(env(map[string]string{
		"GITHUB_ACTIONS": "true", "GITHUB_SERVER_URL": "https://github.com", "GITHUB_REPOSITORY": "acme/pr-status",
		"GITHUB_RUN_ID": "42", "GITHUB_WORKFLOW_REF": "acme/pr-status/.github/workflows/release.yml@refs/tags/v1.0.0",
	}))
	if p.Builder != "github-actions" || p.Repository != "https://github.com/acme/pr-status" || p.Run != "https://github.com/acme/pr-status/actions/runs/42" || p.Workflow != ".github/workflows/release.yml" {
		t.Fatalf("in GitHub Actions: %+v", p)
	}
	if err := p.validate(); err != nil {
		t.Fatal(err)
	}
	// A server that is not HTTPS leaves no links.
	if p := ProvenanceFromEnv(env(map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_SERVER_URL": "http://ghe.local", "GITHUB_REPOSITORY": "a/b", "GITHUB_RUN_ID": "1"})); p.Repository != "" || p.Run != "" {
		t.Fatalf("plain HTTP server: %+v", p)
	}
}

func TestManifestTest(t *testing.T) {
	base := "api = 1\nid = \"x\"\nversion = \"1.0.0\"\n[[actions]]\nid = \"a\"\ncommand = [\"true\"]\n"
	m, err := ParseManifest([]byte(base + "[test]\ncommand = [\"./test.sh\"]\ntimeout = \"30s\"\n"))
	if err != nil || m.Test.TestTimeout() != 30*time.Second {
		t.Fatalf("a [test]: %+v %v", m, err)
	}
	if m, _ := ParseManifest([]byte(base)); m.Test.TestTimeout() != 10*time.Minute {
		t.Fatal("no [test]: the default timeout")
	}
	for name, bad := range map[string]string{
		"no command":   "[test]\ncommand = []\n",
		"bad timeout":  "[test]\ncommand = [\"t\"]\ntimeout = \"soon\"\n",
		"long timeout": "[test]\ncommand = [\"t\"]\ntimeout = \"3h\"\n",
	} {
		if _, err := ParseManifest([]byte(base + bad)); err == nil || !strings.Contains(err.Error(), "test.") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestSignStatementVerifies(t *testing.T) {
	key, priv := testKey(t)
	msg := []byte(`{"id":"x"}`)
	sig := SignStatement(priv, msg)
	if got, err := VerifySignature(sig, msg, []PublicKey{key}); err != nil || got.Fingerprint() != key.Fingerprint() {
		t.Fatalf("SignStatement: %v", err)
	}
	if PublicKeyOf(key.key).Fingerprint() != key.Fingerprint() {
		t.Fatal("PublicKeyOf")
	}
	for _, line := range strings.Split(strings.TrimSpace(sig), "\n") {
		if len(line) > 70 {
			t.Fatalf("armor line longer than 70: %q", line)
		}
	}
}
