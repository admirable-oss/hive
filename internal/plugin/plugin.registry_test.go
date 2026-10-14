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
		"unknown field":       strings.Replace(document, `"schema":1`, `"schema":1,"unexpected":true`, 1),
		"insecure source":     strings.Replace(document, "https://github.com", "http://github.com", 1),
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
