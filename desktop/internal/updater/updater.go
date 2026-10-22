// Package updater updates the desktop app in place from Hive's GitHub
// releases, as `hive update` updates the CLI: a release is installed only
// after desktop-checksums.txt verifies against the release key built into
// the app and the package against its checksum.
//
// Installing hands over to a supervisor, the running binary started again
// with --apply-update (see Supervise): it swaps the new version in, keeps
// the old one beside it, starts the new one, and puts the old one back if
// the new one does not report that it started.
//
// The app reaches Hive only through pkg/hiveapi and imports nothing of
// hive's internal packages, so the little release logic it shares with
// `hive update` (signatures, checksums, versions) is repeated here.
package updater

import (
	"cmp"
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

// Manifest is the signed list of the desktop packages' SHA-256 sums in a
// release; its signature is Manifest + ".sig".
const Manifest = "desktop-checksums.txt"

// maxPackage bounds a downloaded package.
const maxPackage = 512 << 20

// ErrUnsigned means a release's manifest is not signed by the release key.
var ErrUnsigned = errors.New("the release is not signed by the hive release key")

// errNotFound is a 404 from the release server.
var errNotFound = errors.New("404 Not Found")

// ErrNoKey means the app was built without a release key (a development
// build), so it cannot verify releases and does not update itself.
var ErrNoKey = errors.New("this build has no release key: it cannot verify releases, so it does not update itself")

// Config says where releases come from and what is running.
type Config struct {
	// Version is the running app's version ("1.1.0"); "dev" or "" for a
	// development build, which never sees an update as newer.
	Version string
	// PublicKey verifies release manifests. Nil: no updates (ErrNoKey).
	PublicKey ed25519.PublicKey
	// Channel is stable, or preview for release candidates too.
	Channel string
	// GOOS and GOARCH pick the package.
	GOOS, GOARCH string
	// APIURL and DownloadURL override GitHub's URLs (tests, mirrors).
	APIURL, DownloadURL string
	HTTP                *http.Client
}

// Release is a release that has a package for this system.
type Release struct {
	Tag     string `json:"tag"`
	Version string `json:"version"`
	Asset   string `json:"asset"`
	// Newer: the release is newer than the running app.
	Newer bool `json:"newer"`
}

// ParseKey reads a base64 ed25519 public key.
func ParseKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid release key %q", s)
	}
	return ed25519.PublicKey(b), nil
}

// Asset is the name of the desktop package for a system in release tag:
// a universal .zip of the .app for macOS, the installer for Windows, and
// the AppImage for Linux (the .deb and .rpm are updated by their package
// manager).
func Asset(tag, goos, goarch string) string {
	v := strings.TrimPrefix(tag, "v")
	switch goos {
	case "darwin":
		return "hive-desktop_" + v + "_darwin_universal.zip"
	case "windows":
		return "hive-desktop_" + v + "_windows_" + goarch + "_setup.exe"
	}
	return "hive-desktop_" + v + "_" + goos + "_" + goarch + ".AppImage"
}

func (c Config) api() string {
	return cmp.Or(strings.TrimSuffix(c.APIURL, "/"), "https://api.github.com/repos/"+Repository)
}

func (c Config) download() string {
	return cmp.Or(strings.TrimSuffix(c.DownloadURL, "/"), "https://github.com/"+Repository+"/releases/download")
}

func (c Config) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

// Check finds the newest release on the channel and whether it is newer
// than the running app. A release without a package for this system, or
// without desktop packages at all, is reported with an empty Asset.
func (c Config) Check(ctx context.Context) (Release, error) {
	if c.PublicKey == nil {
		return Release{}, ErrNoKey
	}
	tag, err := c.latest(ctx)
	if err != nil {
		return Release{}, err
	}
	rel := Release{Tag: tag, Version: strings.TrimPrefix(tag, "v"), Newer: Newer(tag, c.Version)}
	sums, err := c.manifest(ctx, tag)
	if errors.Is(err, errNotFound) && !errors.Is(err, ErrUnsigned) {
		// No desktop packages in the release, or not yet: the CLI is
		// published first. Nothing to install, which is not an error.
		return rel, nil
	}
	if err != nil {
		return Release{}, err
	}
	if _, err := checksumOf(sums, Asset(tag, c.GOOS, c.GOARCH)); err == nil {
		rel.Asset = Asset(tag, c.GOOS, c.GOARCH)
	}
	return rel, nil
}

func (c Config) latest(ctx context.Context) (string, error) {
	switch c.Channel {
	case "", "stable":
		body, err := c.get(ctx, c.api()+"/releases/latest", 1<<20)
		if err != nil {
			return "", fmt.Errorf("find the latest release: %w", err)
		}
		var rel struct {
			Tag string `json:"tag_name"`
		}
		if err := json.Unmarshal(body, &rel); err != nil || rel.Tag == "" {
			return "", errors.New("find the latest release: GitHub returned no release tag")
		}
		return rel.Tag, nil
	case "preview":
		body, err := c.get(ctx, c.api()+"/releases?per_page=30", 4<<20)
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
			return "", errors.New("no release is published yet")
		}
		return best, nil
	}
	return "", fmt.Errorf("unknown update channel %q (want stable or preview)", c.Channel)
}

// manifest downloads a release's desktop-checksums.txt and verifies its
// signature.
func (c Config) manifest(ctx context.Context, tag string) ([]byte, error) {
	base := c.download() + "/" + tag + "/"
	sums, err := c.get(ctx, base+Manifest, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("%s has no desktop packages (%s: %w)", tag, Manifest, err)
	}
	sig, err := c.get(ctx, base+Manifest+".sig", 4<<10)
	if err != nil {
		return nil, fmt.Errorf("%w (no %s.sig: %w)", ErrUnsigned, Manifest, err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || !ed25519.Verify(c.PublicKey, sums, raw) {
		return nil, ErrUnsigned
	}
	return sums, nil
}

// Download fetches rel's package into dir, verified against the signed
// manifest, and returns its path.
func (c Config) Download(ctx context.Context, rel Release, dir string) (string, error) {
	if c.PublicKey == nil {
		return "", ErrNoKey
	}
	if rel.Asset == "" {
		return "", fmt.Errorf("%s has no desktop package for %s/%s", rel.Tag, c.GOOS, c.GOARCH)
	}
	sums, err := c.manifest(ctx, rel.Tag)
	if err != nil {
		return "", err
	}
	want, err := checksumOf(sums, rel.Asset)
	if err != nil {
		return "", err
	}
	pkg, err := c.get(ctx, c.download()+"/"+rel.Tag+"/"+rel.Asset, maxPackage)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", rel.Asset, err)
	}
	got := sha256.Sum256(pkg)
	if !strings.EqualFold(hex.EncodeToString(got[:]), want) {
		return "", fmt.Errorf("%s does not match its checksum; not installing it", rel.Asset)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, rel.Asset)
	if err := os.WriteFile(path, pkg, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (c Config) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "hive-desktop-update")
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream")
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %w", url, errNotFound)
	}
	if resp.StatusCode != http.StatusOK {
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

// checksumOf finds name's SHA-256 in a checksums file ("<hex>  <name>").
func checksumOf(sums []byte, name string) (string, error) {
	for line := range strings.Lines(string(sums)) {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			if _, err := hex.DecodeString(f[0]); err == nil {
				return f[0], nil
			}
		}
	}
	return "", fmt.Errorf("the release's %s lists no %s", Manifest, name)
}

// Newer reports whether release version a is newer than b, with semantic
// versioning's precedence. A version that does not parse (a development
// build) is older than any that does, and never newer.
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
		return true
	case pb.pre == "":
		return false
	}
	return comparePre(pa.pre, pb.pre) > 0
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

func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range min(len(as), len(bs)) {
		x, y := as[i], bs[i]
		nx, errx := strconv.ParseUint(x, 10, 64)
		ny, erry := strconv.ParseUint(y, 10, 64)
		switch {
		case errx == nil && erry == nil:
			if nx != ny {
				return cmp.Compare(nx, ny)
			}
		case errx == nil:
			return -1
		case erry == nil:
			return 1
		case x != y:
			return strings.Compare(x, y)
		}
	}
	return cmp.Compare(len(as), len(bs))
}
