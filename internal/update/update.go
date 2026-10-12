// Package update replaces the hive binary with a release: it finds the
// latest (or a given) release on GitHub, downloads the archive for this OS
// and architecture, checks it against the release's checksums, and swaps
// the binary in place. Running processes keep the binary they started
// with, so agents' shims are unaffected; `hive update` then restarts the
// daemon, which re-attaches to them.
package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Repository is where releases are published.
const Repository = "admirable-oss/hive"

// errNotFound is a 404 from GitHub.
var errNotFound = errors.New("not found")

// maxDownload bounds an archive's size.
const maxDownload = 256 << 20

// Source is where releases come from.
type Source struct {
	// API is the GitHub API URL of the repository; Download is the base of
	// release asset URLs. Empty means github.com's.
	API, Download string
	HTTP          *http.Client
	// PublicKey verifies the release's checksums.txt.sig (an ed25519
	// signature of checksums.txt). Nil skips the check: builds from source
	// carry no key.
	PublicKey ed25519.PublicKey
}

// ErrUnsigned means a release's checksums are not signed by the release
// key this hive trusts.
var ErrUnsigned = errors.New("the release is not signed by the hive release key")

// ParseKey reads a base64 ed25519 public key.
func ParseKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid release key %q", s)
	}
	return ed25519.PublicKey(b), nil
}

// Verify checks a signature file (base64) of data against key.
func Verify(key ed25519.PublicKey, data, sig []byte) error {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || !ed25519.Verify(key, data, raw) {
		return ErrUnsigned
	}
	return nil
}

func (s Source) api() string {
	if s.API != "" {
		return strings.TrimSuffix(s.API, "/")
	}
	return "https://api.github.com/repos/" + Repository
}

func (s Source) download() string {
	if s.Download != "" {
		return strings.TrimSuffix(s.Download, "/")
	}
	return "https://github.com/" + Repository + "/releases/download"
}

func (s Source) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

// Latest returns the tag of the latest release (e.g. "v0.8.0").
func (s Source) Latest(ctx context.Context) (string, error) {
	body, err := s.get(ctx, s.api()+"/releases/latest", 1<<20)
	if errors.Is(err, errNotFound) {
		return "", fmt.Errorf("no release of %s is published yet", Repository)
	}
	if err != nil {
		return "", fmt.Errorf("find the latest release: %w", err)
	}
	var rel struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", fmt.Errorf("find the latest release: %w", err)
	}
	if rel.Tag == "" {
		return "", errors.New("find the latest release: GitHub returned no release tag")
	}
	return rel.Tag, nil
}

// Channels: stable follows releases; preview also takes pre-releases
// (v1.0.0-rc.1), whichever is newest.
const (
	ChannelStable  = "stable"
	ChannelPreview = "preview"
)

// LatestOn returns the newest release tag on a channel.
func (s Source) LatestOn(ctx context.Context, channel string) (string, error) {
	switch channel {
	case "", ChannelStable:
		return s.Latest(ctx)
	case ChannelPreview:
	default:
		return "", fmt.Errorf("unknown channel %q (want %s or %s)", channel, ChannelStable, ChannelPreview)
	}
	body, err := s.get(ctx, s.api()+"/releases?per_page=30", 4<<20)
	if err != nil {
		return "", fmt.Errorf("list releases: %w", err)
	}
	var rels []struct {
		Tag   string `json:"tag_name"`
		Draft bool   `json:"draft"`
	}
	if err := json.Unmarshal(body, &rels); err != nil {
		return "", fmt.Errorf("list releases: %w", err)
	}
	best := ""
	for _, r := range rels {
		if !r.Draft && r.Tag != "" && (best == "" || Newer(r.Tag, best)) {
			best = r.Tag
		}
	}
	if best == "" {
		return "", fmt.Errorf("no release of %s is published yet", Repository)
	}
	return best, nil
}

// Archive is the name of a release's archive for an OS and architecture.
func Archive(tag, goos, goarch string) string {
	return fmt.Sprintf("hive_%s_%s_%s.tar.gz", strings.TrimPrefix(tag, "v"), goos, goarch)
}

// Fetch downloads release tag's archive for goos/goarch, checks it against
// the release's checksums.txt and returns the hive binary inside.
func (s Source) Fetch(ctx context.Context, tag, goos, goarch string) ([]byte, error) {
	name := Archive(tag, goos, goarch)
	base := s.download() + "/" + tag + "/"
	sums, err := s.get(ctx, base+"checksums.txt", 1<<20)
	if err != nil {
		return nil, fmt.Errorf("download the release's checksums: %w", err)
	}
	if s.PublicKey != nil {
		sig, err := s.get(ctx, base+"checksums.txt.sig", 4<<10)
		if err != nil {
			return nil, fmt.Errorf("%w (no checksums.txt.sig: %w); not installing it", ErrUnsigned, err)
		}
		if err := Verify(s.PublicKey, sums, sig); err != nil {
			return nil, fmt.Errorf("%w; not installing it", err)
		}
	}
	want, err := checksumOf(sums, name)
	if err != nil {
		return nil, err
	}
	archive, err := s.get(ctx, base+name, maxDownload)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", name, err)
	}
	got := sha256.Sum256(archive)
	if !strings.EqualFold(hex.EncodeToString(got[:]), want) {
		return nil, fmt.Errorf("%s does not match its checksum; not installing it", name)
	}
	return Extract(archive)
}

func (s Source) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "hive-update")
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream")
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("%s: %w", url, errNotFound)
	default:
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s: larger than %d bytes", url, limit)
	}
	return body, nil
}

// checksumOf finds name's SHA-256 in a checksums.txt ("<hex>  <name>").
func checksumOf(sums []byte, name string) (string, error) {
	for line := range strings.Lines(string(sums)) {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			if _, err := hex.DecodeString(f[0]); err == nil {
				return f[0], nil
			}
		}
	}
	return "", fmt.Errorf("the release's checksums.txt lists no %s (no build for this system?)", name)
}

// Extract returns the hive binary from a release archive (.tar.gz).
func Extract(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("read the archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the archive holds no hive binary")
		}
		if err != nil {
			return nil, fmt.Errorf("read the archive: %w", err)
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == "hive" {
			return io.ReadAll(io.LimitReader(tr, maxDownload))
		}
	}
}

// Install replaces the binary at target with bin (Stage, then Commit).
func Install(target string, bin []byte) error {
	staged, err := Stage(target, bin)
	if err != nil {
		return err
	}
	return Commit(staged, target)
}

// Stage writes bin as an executable file beside target, where it can be
// tried before Commit puts it in place. The caller removes it if it does
// not Commit.
func Stage(target string, bin []byte) (string, error) {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".hive-update-*")
	if err != nil {
		return "", fmt.Errorf("cannot write to %s (%w); update hive with the permissions it was installed with", dir, err)
	}
	_, err = tmp.Write(bin)
	if err == nil {
		err = tmp.Sync()
	}
	err = errors.Join(err, tmp.Close())
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o755)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// Commit renames a staged binary over target, so target is never missing
// or half written, and processes running the old binary keep it.
func Commit(staged, target string) error {
	if err := os.Rename(staged, target); err != nil {
		_ = os.Remove(staged)
		return err
	}
	return nil
}

// Managed names the package manager that installed path ("Homebrew",
// "Nix"), whose own upgrade must be used instead; "" for none.
func Managed(path string) string {
	switch {
	case strings.Contains(path, "/Cellar/"):
		return "Homebrew"
	case strings.HasPrefix(path, "/nix/store/"):
		return "Nix"
	}
	return ""
}

// Newer reports whether release version a is newer than b. Versions are
// "v1.2.3" with an optional pre-release ("-rc.1"), which sorts before the
// release. A version that does not parse (a "dev" build) is older than any
// that does.
func Newer(a, b string) bool {
	pa, oka := parse(a)
	pb, okb := parse(b)
	switch {
	case !oka:
		return false
	case !okb:
		return true
	}
	for i := range 3 {
		if pa.n[i] != pb.n[i] {
			return pa.n[i] > pb.n[i]
		}
	}
	switch {
	case pa.pre == pb.pre:
		return false
	case pa.pre == "":
		return true // a release is newer than its pre-releases
	case pb.pre == "":
		return false
	}
	return pa.pre > pb.pre
}

type version struct {
	n   [3]int
	pre string
}

func parse(v string) (version, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "+")
	core, pre, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var out version
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}, false
		}
		out.n[i] = n
	}
	out.pre = pre
	return out, true
}
