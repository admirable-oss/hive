package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSSHSignVerifiesWithSSHKeygen: OpenSSH accepts the signature the
// installers check, and only for the signed data and the hive-release
// namespace.
func TestSSHSignVerifiesWithSSHKeygen(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen not installed")
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	data := []byte("0123abcd  hive_1.1.0_linux_amd64.tar.gz\n")
	sigPath := filepath.Join(dir, "checksums.txt.sshsig")
	if err := os.WriteFile(sigPath, SSHSign(priv, data), 0o600); err != nil {
		t.Fatal(err)
	}
	signers := filepath.Join(dir, "allowed_signers")
	if err := os.WriteFile(signers, []byte(AllowedSigners(pub)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	verify := func(namespace string, signed []byte) error {
		cmd := exec.Command(keygen, "-Y", "verify", "-f", signers, "-I", Namespace, "-n", namespace, "-s", sigPath)
		cmd.Stdin = strings.NewReader(string(signed))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("ssh-keygen: %s", out)
		}
		return err
	}
	if err := verify(Namespace, data); err != nil {
		t.Fatalf("a release signature does not verify: %v", err)
	}
	if err := verify(Namespace, append(data, 'x')); err == nil {
		t.Fatal("altered data verified")
	}
	if err := verify("file", data); err == nil {
		t.Fatal("verified in another namespace")
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := os.WriteFile(signers, []byte(AllowedSigners(other)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verify(Namespace, data); err == nil {
		t.Fatal("verified against another key")
	}
}
