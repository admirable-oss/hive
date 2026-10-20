package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryFetchSearchAndValidation(t *testing.T) {
	document := `{"schema":1,"plugins":[{"id":"pr-status","name":"PR Status","description":"Pull request updates","categories":["developer-tools"],"repository":"https://github.com/example/pr-status","license":"MIT","maintainers":[{"name":"Hive Team"}],"releases":[{"version":"1.0.0","ref":"v1.0.0","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","checksum":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","published_at":"2026-10-14T00:00:00Z"}]}]}`
	catalog, err := registryWithBody(document).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := catalog.Search("pull request", "developer-tools"); len(got) != 1 || got[0].ID != "pr-status" {
		t.Fatalf("Search() = %+v", got)
	}
	if _, ok := catalog.Get("pr-status"); !ok {
		t.Fatal("Get() did not return registered plugin")
	}

	start := strings.Index(document, `{"id":"pr-status"`)
	end := strings.LastIndex(document, `]}`)
	entry := document[start:end]
	duplicate := document[:start] + entry + "," + entry + "]}"
	for name, invalid := range map[string]string{
		"unsupported schema":  strings.Replace(document, `"schema":1`, `"schema":2`, 1),
		"duplicate plugin ID": duplicate,
		"insecure source":     strings.Replace(document, "https://github.com", "http://github.com", 1),
		"reversed Hive range": strings.Replace(document, `"repository":"https://github.com/example/pr-status"`, `"repository":"https://github.com/example/pr-status","min_hive_version":"2.0.0","max_hive_version":"1.0.0"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := registryWithBody(invalid).Fetch(context.Background()); err == nil {
				t.Fatal("Fetch() accepted an invalid registry")
			}
		})
	}
}

type registryRoundTripper func(*http.Request) (*http.Response, error)

func (f registryRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func registryWithBody(body string) *RegistryClient {
	c := NewRegistryClient("https://registry.example/plugins.json")
	c.HTTP = &http.Client{Transport: registryRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	return c
}

// TestRegistryIgnoresFieldsItDoesNotKnow: the catalog's schema grows by
// additions, so a newer catalog's fields do not stop this hive reading it.
func TestRegistryIgnoresFieldsItDoesNotKnow(t *testing.T) {
	document := strings.NewReplacer(
		`"schema":1`, `"schema":1,"generated_by":"a newer registry"`,
		`"license":"MIT"`, `"license":"MIT","support":{"security":"https://example.org/security"}`,
		`"published_at"`, `"sbom":"https://example.org/sbom.json","published_at"`,
	).Replace(testRegistry)
	catalog, err := registryWithBody(document).Fetch(context.Background())
	if err != nil {
		t.Fatalf("a catalog with new fields: %v", err)
	}
	if p, ok := catalog.Get("pr-status"); !ok || len(p.Releases) != 2 {
		t.Fatalf("catalog = %+v", catalog)
	}
}

const testRegistry = `{"schema":1,"plugins":[{"id":"pr-status","name":"PR Status","repository":"https://github.com/example/pr-status","license":"MIT","maintainers":[{"name":"Hive Team"}],"releases":[` +
	`{"version":"1.0.0","ref":"v1.0.0","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","checksum":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","published_at":"2026-10-14T00:00:00Z"},` +
	`{"version":"1.1.0","ref":"v1.1.0","commit":"cccccccccccccccccccccccccccccccccccccccc","checksum":"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","published_at":"2026-10-16T00:00:00Z"}]}]}`

// registryServing serves the catalog at /plugins/registry.json and, when
// revocations is not empty, the revocations document beside it; status
// overrides the revocations document's status.
func registryServing(revocations string, status int) *RegistryClient {
	c := NewRegistryClient("https://registry.example/plugins/registry.json")
	c.HTTP = &http.Client{Transport: registryRoundTripper(func(r *http.Request) (*http.Response, error) {
		code, body := http.StatusOK, testRegistry
		switch r.URL.Path {
		case "/plugins/registry.json":
		case "/plugins/" + RevocationsFile:
			code, body = status, revocations
			if revocations == "" && status == 0 {
				code = http.StatusNotFound
			}
			if code == 0 {
				code = http.StatusOK
			}
		default:
			code, body = http.StatusNotFound, ""
		}
		return &http.Response{StatusCode: code, Status: http.StatusText(code), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	return c
}

func TestRegistryRevocations(t *testing.T) {
	ctx := context.Background()
	revoke := func(versions ...string) string {
		var recs []string
		for _, v := range versions {
			recs = append(recs, `{"id":"pr-status","version":"`+v+`","reason":"leaks tokens","advisory":"https://github.com/example/pr-status/security/advisories/GHSA-x","revoked_at":"2026-10-18T00:00:00Z"}`)
		}
		return `{"schema":1,"releases":[` + strings.Join(recs, ",") + `]}`
	}

	t.Run("none published", func(t *testing.T) {
		catalog, err := registryServing("", 0).Fetch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		p, _ := catalog.Get("pr-status")
		if latest, ok := p.Latest(); !ok || latest.Version != "1.1.0" {
			t.Fatalf("latest = %+v, %v", latest, ok)
		}
	})
	t.Run("the newest release is revoked", func(t *testing.T) {
		catalog, err := registryServing(revoke("1.1.0"), 0).Fetch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		p, _ := catalog.Get("pr-status")
		if len(p.Releases) != 2 || p.Releases[0].Revoked == nil || p.Releases[0].Revoked.Reason != "leaks tokens" {
			t.Fatalf("the revoked release is not kept and marked: %+v", p.Releases)
		}
		if latest, ok := p.Latest(); !ok || latest.Version != "1.0.0" {
			t.Fatalf("latest = %+v, %v; want the newest release not revoked", latest, ok)
		}
		if _, ok := catalog.Revocation("pr-status", "v1.1.0"); !ok {
			t.Fatal("Revocation() does not find it by tag")
		}
	})
	t.Run("every release is revoked", func(t *testing.T) {
		catalog, err := registryServing(revoke("1.0.0", "1.1.0"), 0).Fetch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		p, _ := catalog.Get("pr-status")
		if latest, ok := p.Latest(); ok {
			t.Fatalf("latest = %+v; every release is revoked", latest)
		}
	})
	t.Run("a release no longer listed", func(t *testing.T) {
		catalog, err := registryServing(revoke("0.9.0"), 0).Fetch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := catalog.Revocation("pr-status", "0.9.0"); !ok {
			t.Fatal("the revocation of an unlisted release is lost")
		}
	})
	for name, tt := range map[string]struct {
		doc    string
		status int
	}{
		"unreadable revocations": {"", http.StatusInternalServerError},
		"forbidden revocations":  {"", http.StatusForbidden},
		"unsupported schema":     {`{"schema":2,"releases":[]}`, 0},
		"no reason":              {strings.Replace(revoke("1.1.0"), `"leaks tokens"`, `""`, 1), 0},
		"insecure advisory":      {strings.Replace(revoke("1.1.0"), "https://github.com", "http://github.com", 1), 0},
		"malformed":              {`{"schema":1,"releases":[`, 0},
	} {
		t.Run(name+" fail closed", func(t *testing.T) {
			if _, err := registryServing(tt.doc, tt.status).Fetch(ctx); err == nil {
				t.Fatal("Fetch() went on without knowing what is revoked")
			}
		})
	}
}

func TestFetchedVerifyArchiveChecksum(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	source := writePlugin(t, sample, map[string]string{"watch.sh": "#!/bin/sh\n"}, true)
	f, err := NewStore(filepath.Join(t.TempDir(), "plugins")).Fetch(context.Background(), source, "")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Discard()

	archive, err := exec.Command("git", "-C", f.dir, "archive", "--format=tar", f.Commit).Output()
	if err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256(archive)
	want := "sha256:" + hex.EncodeToString(checksum[:])
	if err := f.VerifyArchiveChecksum(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if err := f.VerifyArchiveChecksum(context.Background(), "sha256:"+strings.Repeat("0", 64)); err == nil {
		t.Fatal("VerifyArchiveChecksum() accepted an incorrect digest")
	}
}

func TestLandingPublishedRegistry(t *testing.T) {
	registryPath := filepath.Join("..", "..", "..", "hive-landing", "public", "plugins", "registry.json")
	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Skipf("landing registry not found at %s: %v", registryPath, err)
	}
	catalog, err := registryWithBody(string(data)).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() failed to validate landing registry: %v", err)
	}
	if len(catalog.Plugins) == 0 {
		t.Fatal("landing registry contains 0 plugins")
	}
	for _, p := range catalog.Plugins {
		if _, ok := catalog.Get(p.ID); !ok {
			t.Fatalf("catalog.Get(%q) failed", p.ID)
		}
	}
}

// TestPublishedRevocations: the repository's revocations document, which
// the website serves beside the catalog, is one this hive accepts.
func TestPublishedRevocations(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "registry", RevocationsFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registryServing(string(data), http.StatusOK).Fetch(context.Background()); err != nil {
		t.Fatalf("registry/%s: %v", RevocationsFile, err)
	}
}

func TestIsHex(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want bool
	}{
		{name: "digits", in: "0123456789", want: true},
		{name: "lowercase", in: "abcdef", want: true},
		{name: "uppercase", in: "ABCDEF", want: true},
		{name: "mixed", in: "a0B1f9", want: true},
		{name: "invalid", in: "a0g1", want: false},
		{name: "empty", in: "", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isHex(tc.in); got != tc.want {
				t.Fatalf("isHex(%q) = %t, want %t", tc.in, got, tc.want)
			}
		})
	}
}
