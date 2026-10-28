package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"
)

// DefaultRegistryURL is the public Hive plugin catalog. Set
// HIVE_PLUGIN_REGISTRY_URL to use a self-hosted or development registry.
const DefaultRegistryURL = "https://hive.admir-saheta.com/plugins/registry.json"

// Schema2File is the schema 2 catalog's name, beside the schema 1 catalog.
// Hive 1.7 and later read it first and fall back to schema 1 when the
// registry does not serve it (404).
const Schema2File = "registry-2.json"

const maxRegistryBytes = 4 << 20

// Registry is the versioned catalog document served as JSON.
//
// Schema 1 grows only by additions, and Fetch ignores fields it does not
// know, so a newer catalog stays readable. (Hive before 1.0.10 rejected
// unknown fields: the published catalog must not add any while those
// releases are supported. See docs/compatibility.md.)
//
// Schema 2 (registry-2.json, beside it) adds publishers and their keys,
// signatures and build provenance per release, per-plugin release refs,
// and the Hive releases each release was tested against.
type Registry struct {
	Schema int `json:"schema"`
	// Publishers are schema 2's: who publishes, in which tier, with which
	// keys.
	Publishers []Publisher `json:"publishers,omitempty"`
	Plugins    []Listing   `json:"plugins"`
	// Revocations are every release the registry has withdrawn, listed or
	// not, from the revocations document beside the catalog, including
	// the releases signed by a revoked key.
	Revocations []Revocation `json:"revocations,omitempty"`
	// RevokedKeys are the publisher keys the registry distrusts.
	RevokedKeys []KeyRevocation `json:"revoked_keys,omitempty"`
}

// Listing is the public metadata for one plugin in the catalog.
type Listing struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Description    string       `json:"description,omitempty"`
	Screenshots    []string     `json:"screenshots,omitempty"`
	Install        string       `json:"installation,omitempty"`
	Configuration  string       `json:"configuration,omitempty"`
	Categories     []string     `json:"categories,omitempty"`
	Maintainers    []Maintainer `json:"maintainers,omitempty"`
	License        string       `json:"license,omitempty"`
	Repository     string       `json:"repository"`
	Documentation  string       `json:"documentation,omitempty"`
	Issues         string       `json:"issues,omitempty"`
	Platforms      []string     `json:"platforms,omitempty"`
	MinHiveVersion string       `json:"min_hive_version,omitempty"`
	MaxHiveVersion string       `json:"max_hive_version,omitempty"`
	API            string       `json:"plugin_api,omitempty"`
	// Publisher is the schema 2 publisher's ID; Tier is its tier (in
	// schema 1, derived from Official and Verified, which schema 2 derives
	// from the tier in turn).
	Publisher  string    `json:"publisher,omitempty"`
	Tier       string    `json:"tier,omitempty"`
	Featured   bool      `json:"featured,omitempty"`
	Official   bool      `json:"official,omitempty"`
	Verified   bool      `json:"verified,omitempty"`
	Deprecated bool      `json:"deprecated,omitempty"`
	Archived   bool      `json:"archived,omitempty"`
	Releases   []Release `json:"releases"`
}

// Maintainer identifies a person or organization responsible for a listing.
type Maintainer struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// Release points at an immutable source revision. Commit is checked against
// the fetched repository before any plugin code is installed.
type Release struct {
	Version     string    `json:"version"`
	Ref         string    `json:"ref"`
	Commit      string    `json:"commit"`
	Checksum    string    `json:"checksum,omitempty"`
	Subdir      string    `json:"subdir,omitempty"`
	Changelog   string    `json:"changelog,omitempty"`
	PublishedAt time.Time `json:"published_at"`
	// PluginAPI and MinHiveVersion are schema 2's, per release: a release
	// this hive cannot run is never picked as the one to install.
	PluginAPI      int    `json:"plugin_api,omitempty"`
	MinHiveVersion string `json:"min_hive_version,omitempty"`
	// Signature is the publisher's SSH signature of the release statement
	// (ReleaseStatement); Provenance is how it was built; Tested are the
	// Hive releases it was tested against.
	Signature  string         `json:"signature,omitempty"`
	Provenance *Provenance    `json:"provenance,omitempty"`
	Tested     []CompatResult `json:"tested,omitempty"`
	// SignedBy is the fingerprint of the publisher key whose signature
	// verified, and SignatureError why a signature did not: set by Fetch,
	// never taken from the registry.
	SignedBy       string `json:"signed_by,omitempty"`
	SignatureError string `json:"signature_error,omitempty"`
	// Revoked is set when the registry has withdrawn the release. It stays
	// listed, for the record, but is never installed or updated to.
	Revoked *Revocation `json:"revoked,omitempty"`
}

// CompatResult is a release tested against a Hive release (the registry's
// plugin-compat workflow, or `hive plugin test`).
type CompatResult struct {
	Hive   string    `json:"hive"`
	Result string    `json:"result"` // pass or fail
	At     time.Time `json:"at"`
	Report string    `json:"report,omitempty"`
}

// Revocation withdraws a published release: for a security problem, a
// compromised maintainer account or a broken build. Revocations are a
// document of their own beside the catalog (revocations.json), so clients
// older than 1.0.10, which reject catalog fields they do not know, keep
// reading the catalog.
type Revocation struct {
	ID        string    `json:"id"`
	Version   string    `json:"version"`
	Reason    string    `json:"reason"`
	Advisory  string    `json:"advisory,omitempty"`
	RevokedAt time.Time `json:"revoked_at"`
}

// RevocationsFile is the revocations document's name, beside the catalog.
const RevocationsFile = "revocations.json"

type revocationDocument struct {
	Schema   int          `json:"schema"`
	Releases []Revocation `json:"releases"`
	// Keys distrusts publisher keys (Hive 1.7 and later; earlier releases
	// ignore the field).
	Keys []KeyRevocation `json:"keys,omitempty"`
}

// RegistryClient retrieves and validates the public catalog.
type RegistryClient struct {
	URL  string
	HTTP *http.Client
}

// NewRegistryClient creates a bounded client for a registry endpoint.
func NewRegistryClient(endpoint string) *RegistryClient {
	if endpoint == "" {
		endpoint = DefaultRegistryURL
	}
	return &RegistryClient{URL: endpoint, HTTP: &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 0 && via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("refusing HTTPS-to-HTTP registry redirect")
			}
			return nil
		},
	}}
}

// Fetch downloads the catalog and the registry's revocations, and rejects
// unsupported schemas and conflicting or malformed plugin records.
//
// It reads the schema 2 catalog beside the configured one first, and the
// configured one when the registry serves no schema 2 (404). Schema 2
// signatures are checked here, against the publishers' keys: each release
// carries the key that signed it, or why its signature did not verify.
//
// A registry without a revocations document (404) has revoked nothing; one
// that cannot be read is an error, so a withdrawn release is never
// installed because its revocation was out of reach.
func (c *RegistryClient) Fetch(ctx context.Context) (Registry, error) {
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return Registry{}, fmt.Errorf("invalid plugin registry URL %q", c.URL)
	}
	body, err := c.catalog(ctx, u)
	if err != nil {
		return Registry{}, err
	}
	var catalog Registry
	if err := decodeOne(body, &catalog); err != nil {
		return Registry{}, fmt.Errorf("decode plugin registry: %w", err)
	}
	if catalog.Schema != 1 && catalog.Schema != 2 {
		return Registry{}, fmt.Errorf("unsupported plugin registry schema %d", catalog.Schema)
	}
	catalog.Revocations, catalog.RevokedKeys = nil, nil
	publishers := map[string]*Publisher{}
	if catalog.Schema == 1 {
		catalog.Publishers = nil
	}
	for i := range catalog.Publishers {
		p := &catalog.Publishers[i]
		if err := p.validate(); err != nil {
			return Registry{}, err
		}
		if publishers[p.ID] != nil {
			return Registry{}, fmt.Errorf("duplicate publisher %q in registry", p.ID)
		}
		publishers[p.ID] = p
	}
	seen := make(map[string]bool, len(catalog.Plugins))
	for i := range catalog.Plugins {
		p := &catalog.Plugins[i]
		if !idPattern.MatchString(p.ID) || p.Name == "" || p.Repository == "" || p.License == "" || len(p.Maintainers) == 0 || len(p.Releases) == 0 {
			return Registry{}, fmt.Errorf("invalid plugin registry entry at plugins[%d]", i)
		}
		if !isHTTPSURL(p.Repository) {
			return Registry{}, fmt.Errorf("plugin %s repository must be an HTTPS URL", p.ID)
		}
		for _, link := range append(append(slices.Clone(p.Screenshots), p.Documentation, p.Issues), maintainerURLs(p.Maintainers)...) {
			if link != "" && !isHTTPSURL(link) {
				return Registry{}, fmt.Errorf("plugin %s contains a non-HTTPS external link", p.ID)
			}
		}
		if (p.MinHiveVersion != "" && !versionPattern.MatchString(p.MinHiveVersion)) || (p.MaxHiveVersion != "" && !versionPattern.MatchString(p.MaxHiveVersion)) {
			return Registry{}, fmt.Errorf("plugin %s has an invalid Hive compatibility range", p.ID)
		}
		if p.MinHiveVersion != "" && p.MaxHiveVersion != "" && newer(p.MinHiveVersion, p.MaxHiveVersion) {
			return Registry{}, fmt.Errorf("plugin %s has a reversed Hive compatibility range", p.ID)
		}
		for _, maintainer := range p.Maintainers {
			if strings.TrimSpace(maintainer.Name) == "" {
				return Registry{}, fmt.Errorf("plugin %s has a maintainer without a name", p.ID)
			}
		}
		if seen[p.ID] {
			return Registry{}, fmt.Errorf("duplicate plugin ID %q in registry", p.ID)
		}
		seen[p.ID] = true
		// Who stands behind it: schema 2 says through its publisher, schema
		// 1 through its flags.
		var publisher *Publisher
		if catalog.Schema == 2 {
			if publisher = publishers[p.Publisher]; publisher == nil {
				return Registry{}, fmt.Errorf("plugin %s names publisher %q, which the registry does not list", p.ID, p.Publisher)
			}
			p.Tier, p.Official, p.Verified = publisher.Tier, publisher.Tier == TierOfficial, publisher.Tier == TierVerified
		} else {
			p.Publisher, p.Tier = "", tierOfSchema1(*p)
		}
		releases := make(map[string]bool, len(p.Releases))
		for j := range p.Releases {
			r := &p.Releases[j]
			if !versionPattern.MatchString(r.Version) || !refMatchesVersion(r.Ref, r.Version, catalog.Schema == 2) || len(r.Commit) != 40 || r.PublishedAt.IsZero() || !strings.HasPrefix(strings.ToLower(r.Checksum), "sha256:") || len(r.Checksum) != len("sha256:")+64 || !isHex(strings.TrimPrefix(strings.ToLower(r.Checksum), "sha256:")) {
				return Registry{}, fmt.Errorf("invalid release %s for plugin %s", r.Version, p.ID)
			}
			if releases[r.Version] {
				return Registry{}, fmt.Errorf("duplicate release %s for plugin %s", r.Version, p.ID)
			}
			releases[r.Version] = true
			if !isHex(r.Commit) {
				return Registry{}, fmt.Errorf("release %s for plugin %s has invalid commit", r.Version, p.ID)
			}
			if r.Subdir != "" {
				if path.IsAbs(r.Subdir) || path.Clean(r.Subdir) != r.Subdir || strings.Contains(r.Subdir, "\\") || r.Subdir == "." || r.Subdir == ".." || strings.HasPrefix(r.Subdir, "../") {
					return Registry{}, fmt.Errorf("release %s for plugin %s has invalid subdir %q", r.Version, p.ID, r.Subdir)
				}
			}
			// Only Fetch decides these, and the revocations below.
			r.SignedBy, r.SignatureError, r.Revoked = "", "", nil
			if catalog.Schema == 1 {
				r.PluginAPI, r.MinHiveVersion, r.Signature, r.Provenance, r.Tested = 0, "", "", nil, nil
				continue
			}
			if r.PluginAPI < 0 || (r.MinHiveVersion != "" && !versionPattern.MatchString(r.MinHiveVersion)) {
				return Registry{}, fmt.Errorf("release %s for plugin %s has an invalid plugin API or Hive version", r.Version, p.ID)
			}
			if err := r.Provenance.validate(); err != nil {
				return Registry{}, fmt.Errorf("release %s for plugin %s: %w", r.Version, p.ID, err)
			}
			for _, t := range r.Tested {
				if !versionPattern.MatchString(t.Hive) || (t.Result != "pass" && t.Result != "fail") || (t.Report != "" && !isHTTPSURL(t.Report)) {
					return Registry{}, fmt.Errorf("release %s for plugin %s has an invalid test result", r.Version, p.ID)
				}
			}
			if r.Signature != "" {
				if key, err := VerifySignature(r.Signature, ReleaseStatement(p.ID, *r), publisher.publicKeys()); err != nil {
					r.SignatureError = err.Error()
				} else {
					r.SignedBy = key.Fingerprint()
				}
			}
		}
		slices.SortFunc(p.Releases, func(a, b Release) int {
			return compareVersions(b.Version, a.Version)
		})
	}
	slices.SortFunc(catalog.Plugins, func(a, b Listing) int { return strings.Compare(a.ID, b.ID) })
	revoked, keys, err := c.revocations(ctx, u)
	if err != nil {
		return Registry{}, err
	}
	catalog.Revocations, catalog.RevokedKeys = revoked, keys
	for i := range catalog.Revocations {
		if err := catalog.Revocations[i].validate(); err != nil {
			return Registry{}, err
		}
	}
	for _, k := range catalog.RevokedKeys {
		if err := k.validate(); err != nil {
			return Registry{}, err
		}
	}
	// A revoked key revokes the listed releases it signed from the time it
	// is distrusted.
	for _, p := range catalog.Plugins {
		for _, r := range p.Releases {
			if r.SignedBy == "" {
				continue
			}
			if _, ok := catalog.Revocation(p.ID, r.Version); ok {
				continue
			}
			for _, k := range catalog.RevokedKeys {
				if k.Fingerprint == r.SignedBy && !r.PublishedAt.Before(k.Since) {
					catalog.Revocations = append(catalog.Revocations, Revocation{ID: p.ID, Version: r.Version, Reason: "signed with a revoked publisher key (" + k.Fingerprint + "): " + k.Reason, Advisory: k.Advisory, RevokedAt: k.Since})
					break
				}
			}
		}
	}
	for i := range catalog.Plugins {
		p := &catalog.Plugins[i]
		for j := range p.Releases {
			if r, ok := catalog.Revocation(p.ID, p.Releases[j].Version); ok {
				p.Releases[j].Revoked = &r
			}
		}
	}
	return catalog, nil
}

// catalog reads the schema 2 catalog beside u, or u when the registry has
// none. Only a missing schema 2 catalog falls back: one that fails
// otherwise is an error, so nobody between Hive and the registry can make
// it read the catalog without signatures.
func (c *RegistryClient) catalog(ctx context.Context, u *url.URL) ([]byte, error) {
	if path.Base(u.Path) != Schema2File {
		body, status, err := c.get(ctx, sibling(u, Schema2File))
		switch {
		case err == nil:
			return body, nil
		case status != http.StatusNotFound && status != http.StatusGone:
			return nil, err
		}
	}
	body, _, err := c.get(ctx, u.String())
	return body, err
}

// sibling is the document name beside the catalog at u.
func sibling(u *url.URL, name string) string {
	v := *u
	dir := path.Dir(v.Path)
	if dir == "." {
		dir = "/"
	}
	v.Path, v.RawPath, v.RawQuery = path.Join(dir, name), "", ""
	return v.String()
}

// revocations reads the revocations document beside the catalog at u.
func (c *RegistryClient) revocations(ctx context.Context, catalog *url.URL) ([]Revocation, []KeyRevocation, error) {
	body, status, err := c.get(ctx, sibling(catalog, RevocationsFile))
	if status == http.StatusNotFound || status == http.StatusGone {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("cannot tell which plugin releases are revoked: %w", err)
	}
	var doc revocationDocument
	if err := decodeOne(body, &doc); err != nil {
		return nil, nil, fmt.Errorf("decode plugin revocations: %w", err)
	}
	if doc.Schema != 1 {
		return nil, nil, fmt.Errorf("unsupported plugin revocations schema %d", doc.Schema)
	}
	return doc.Releases, doc.Keys, nil
}

// refMatchesVersion checks that a release's git ref names its version:
// "v1.2.0" or "1.2.0" for version 1.2.0, and in schema 2 also a per-plugin
// ref ending in one ("pr-status/v1.2.0"), so plugins sharing a repository
// tag their releases apart.
func refMatchesVersion(ref, version string, perPlugin bool) bool {
	if perPlugin {
		if i := strings.LastIndexByte(ref, '/'); i >= 0 {
			if i == 0 || strings.Contains(ref[:i], "..") || strings.HasPrefix(ref, "/") {
				return false
			}
			ref = ref[i+1:]
		}
	}
	return ref != "" && strings.TrimPrefix(ref, "v") == strings.TrimPrefix(version, "v")
}

func (r Revocation) validate() error {
	if !idPattern.MatchString(r.ID) || !versionPattern.MatchString(r.Version) || strings.TrimSpace(r.Reason) == "" || r.RevokedAt.IsZero() || (r.Advisory != "" && !isHTTPSURL(r.Advisory)) {
		return fmt.Errorf("invalid revocation of plugin %q release %q", r.ID, r.Version)
	}
	return nil
}

// get fetches a registry document, bounded in size; status is the HTTP
// status when there was a response.
func (c *RegistryClient) get(ctx context.Context, target string) (body []byte, status int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("plugin registry unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("plugin registry returned %s", resp.Status)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxRegistryBytes+1))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read plugin registry: %w", err)
	}
	if len(body) > maxRegistryBytes {
		return nil, resp.StatusCode, fmt.Errorf("plugin registry exceeds %d bytes", maxRegistryBytes)
	}
	return body, resp.StatusCode, nil
}

// decodeOne decodes exactly one JSON value, ignoring fields v does not
// know (registry documents grow by additions).
func decodeOne(body []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(v); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

// Latest returns the newest release that is not revoked; false when every
// release is.
func (p Listing) Latest() (Release, bool) {
	for _, r := range p.Releases {
		if r.Revoked == nil {
			return r, true
		}
	}
	return Release{}, false
}

// LatestFor returns the newest release this hive can install: not revoked,
// on a plugin API it runs, and needing no newer Hive. False when none is.
func (p Listing) LatestFor(hiveVersion string) (Release, bool) {
	for _, r := range p.Releases {
		if r.Revoked != nil || (r.PluginAPI != 0 && !slices.Contains(SupportedAPIs, r.PluginAPI)) {
			continue
		}
		if r.MinHiveVersion != "" && !devBuild(hiveVersion) && newer(r.MinHiveVersion, hiveVersion) {
			continue
		}
		return r, true
	}
	return Release{}, false
}

// Revocation returns the registry's revocation of a plugin's release,
// whether or not the release is still listed.
func (r Registry) Revocation(id, version string) (Revocation, bool) {
	for _, rev := range r.Revocations {
		if rev.ID == id && strings.TrimPrefix(rev.Version, "v") == strings.TrimPrefix(version, "v") {
			return rev, true
		}
	}
	return Revocation{}, false
}

func isHTTPSURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == ""
}

func maintainerURLs(maintainers []Maintainer) []string {
	urls := make([]string, 0, len(maintainers))
	for _, m := range maintainers {
		urls = append(urls, m.URL)
	}
	return urls
}

// SupportsHive checks the listing's inclusive minimum and maximum versions.
func (p Listing) SupportsHive(version string) error {
	if devBuild(version) {
		return nil
	}
	if p.MinHiveVersion != "" && newer(p.MinHiveVersion, version) {
		return fmt.Errorf("plugin %s needs Hive %s or newer (this is %s)", p.ID, p.MinHiveVersion, version)
	}
	if p.MaxHiveVersion != "" && newer(version, p.MaxHiveVersion) {
		return fmt.Errorf("plugin %s supports Hive through %s (this is %s)", p.ID, p.MaxHiveVersion, version)
	}
	return nil
}

func compareVersions(a, b string) int {
	if newer(a, b) {
		return 1
	}
	if newer(b, a) {
		return -1
	}
	return 0
}

// Search returns listings matching query and optional category.
func (r Registry) Search(query, category string) []Listing {
	query, category = strings.ToLower(strings.TrimSpace(query)), strings.ToLower(strings.TrimSpace(category))
	out := make([]Listing, 0)
	for _, p := range r.Plugins {
		if category != "" && !containsFold(p.Categories, category) {
			continue
		}
		text := strings.ToLower(strings.Join([]string{p.ID, p.Name, p.Description, strings.Join(p.Categories, " ")}, " "))
		if query == "" || strings.Contains(text, query) {
			out = append(out, p)
		}
	}
	return out
}

// Get returns a listing by ID.
func (r Registry) Get(id string) (Listing, bool) {
	i := slices.IndexFunc(r.Plugins, func(p Listing) bool { return p.ID == id })
	if i < 0 {
		return Listing{}, false
	}
	return r.Plugins[i], true
}

func containsFold(values []string, want string) bool {
	return slices.ContainsFunc(values, func(v string) bool { return strings.EqualFold(v, want) })
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
