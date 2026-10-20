//go:build !windows

package distribution_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// release is a fake GitHub release served to a copy of install.sh.
type release struct {
	tag    string
	signer ed25519.PrivateKey // nil: no checksums.txt.sshsig
}

// releaseSign runs tools/release-sign with key, as the release workflow does.
func releaseSign(t *testing.T, key ed25519.PrivateKey, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"run", "../tools/release-sign"}, args...)...)
	cmd.Env = append(os.Environ(), "HIVE_RELEASE_SIGNING_KEY="+base64.StdEncoding.EncodeToString(key))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("release-sign %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func archive(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "hive", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte(body))
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// serve publishes r and returns the server's URL.
func serve(t *testing.T, r release) string {
	t.Helper()
	osName, arch := runtime.GOOS, runtime.GOARCH
	name := fmt.Sprintf("hive_%s_%s_%s.tar.gz", strings.TrimPrefix(r.tag, "v"), osName, arch)
	arc := archive(t, "#!/bin/sh\necho installed-"+r.tag+"\n")
	sum := sha256.Sum256(arc)
	dir := t.TempDir()
	sums := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(sums, []byte(hex.EncodeToString(sum[:])+"  "+name+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r.signer != nil {
		releaseSign(t, r.signer, "sign-ssh", sums)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags/"+r.tag, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"tag_name": %q}`, r.tag)
	})
	mux.HandleFunc("/dl/"+r.tag+"/"+name, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(arc) })
	mux.HandleFunc("/dl/"+r.tag+"/checksums.txt", func(w http.ResponseWriter, req *http.Request) { http.ServeFile(w, req, sums) })
	if r.signer != nil {
		mux.HandleFunc("/dl/"+r.tag+"/checksums.txt.sshsig", func(w http.ResponseWriter, req *http.Request) { http.ServeFile(w, req, sums+".sshsig") })
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// installer is install.sh pointed at url and trusting key, as the
// published script trusts the release key.
func installer(t *testing.T, url string, key ed25519.PublicKey) string {
	t.Helper()
	script, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	signers := releaseSign(t, nil, "allowed-signers", base64.StdEncoding.EncodeToString(key))
	s := string(script)
	for re, repl := range map[string]string{
		`(?m)^RELEASES_URL=.*$`:    `RELEASES_URL="` + url + `/api"`,
		`(?m)^DOWNLOAD_URL=.*$`:    `DOWNLOAD_URL="` + url + `/dl"`,
		`(?m)^ALLOWED_SIGNERS=.*$`: `ALLOWED_SIGNERS='` + signers + `'`,
	} {
		if !regexp.MustCompile(re).MatchString(s) {
			t.Fatalf("install.sh has no line matching %s", re)
		}
		s = regexp.MustCompile(re).ReplaceAllLiteralString(s, repl)
	}
	path := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// run installs tag with script and returns its output and the installed
// binary's contents ("" when nothing was installed).
func run(t *testing.T, script, tag string, env ...string) (string, string, error) {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("sh", script)
	cmd.Env = append(os.Environ(), "HIVE_VERSION="+tag, "HIVE_INSTALL_DIR="+dir, "HIVE_CONFIG="+filepath.Join(dir, "config.toml"))
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	bin, _ := os.ReadFile(filepath.Join(dir, "hive"))
	return string(out), string(bin), err
}

// pathWithout is a PATH holding the tools install.sh uses, except those
// named, for a system that lacks them.
func pathWithout(t *testing.T, missing ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range []string{"sh", "curl", "tar", "awk", "install", "mktemp", "rm", "mkdir", "uname", "tr", "dirname", "cat", "grep", "sha256sum", "shasum", "openssl", "gzip", "ssh-keygen", "perl"} {
		if strings.Contains(" "+strings.Join(missing, " ")+" ", " "+tool+" ") {
			continue
		}
		if p, err := exec.LookPath(tool); err == nil {
			_ = os.Symlink(p, filepath.Join(dir, tool))
		}
	}
	return "PATH=" + dir
}

func TestInstallerVerifiesTheReleaseSignature(t *testing.T) {
	for _, tool := range []string{"curl", "ssh-keygen", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)

	t.Run("a signed release installs", func(t *testing.T) {
		url := serve(t, release{tag: "v1.1.0", signer: priv})
		out, bin, err := run(t, installer(t, url, pub), "v1.1.0")
		if err != nil || !strings.Contains(bin, "installed-v1.1.0") || !strings.Contains(out, "verified the release signature") {
			t.Fatalf("install: %v\n%s", err, out)
		}
	})
	t.Run("a release signed by another key is refused", func(t *testing.T) {
		url := serve(t, release{tag: "v1.1.0", signer: other})
		out, bin, err := run(t, installer(t, url, pub), "v1.1.0", "HIVE_ALLOW_UNVERIFIED=1")
		if err == nil || bin != "" || !strings.Contains(out, "does not verify") {
			t.Fatalf("a forged release installed (or failed for another reason): %v\n%s", err, out)
		}
	})
	t.Run("an unsigned release is refused", func(t *testing.T) {
		url := serve(t, release{tag: "v1.1.0"})
		out, bin, err := run(t, installer(t, url, pub), "v1.1.0", "HIVE_ALLOW_UNVERIFIED=1")
		if err == nil || bin != "" || !strings.Contains(out, "refusing an unsigned release") {
			t.Fatalf("an unsigned release installed: %v\n%s", err, out)
		}
	})
	t.Run("a release from before installer signatures installs on its checksum", func(t *testing.T) {
		url := serve(t, release{tag: "v1.0.0-rc.1"})
		out, bin, err := run(t, installer(t, url, pub), "v1.0.0-rc.1")
		if err != nil || !strings.Contains(bin, "installed-v1.0.0-rc.1") || !strings.Contains(out, "checking its checksum only") {
			t.Fatalf("install of rc.1: %v\n%s", err, out)
		}
	})
	t.Run("without ssh-keygen it stops, unless told to rely on the checksum", func(t *testing.T) {
		url := serve(t, release{tag: "v1.1.0", signer: priv})
		script := installer(t, url, pub)
		out, bin, err := run(t, script, "v1.1.0", pathWithout(t, "ssh-keygen"))
		if err == nil || bin != "" || !strings.Contains(out, "ssh-keygen (OpenSSH) is not installed") {
			t.Fatalf("installed without checking the signature: %v\n%s", err, out)
		}
		out, bin, err = run(t, script, "v1.1.0", pathWithout(t, "ssh-keygen"), "HIVE_ALLOW_UNVERIFIED=1")
		if err != nil || !strings.Contains(bin, "installed-v1.1.0") || !strings.Contains(out, "checksum only") {
			t.Fatalf("HIVE_ALLOW_UNVERIFIED=1: %v\n%s", err, out)
		}
	})
}
