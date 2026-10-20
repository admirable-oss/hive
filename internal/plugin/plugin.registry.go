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

const maxRegistryBytes = 4 << 20

// Registry is the versioned catalog document served as JSON.
//
// Schema 1 grows only by additions, and Fetch ignores fields it does not
// know, so a newer catalog stays readable. (Hive before 1.0.10 rejected
// unknown fields: the published catalog must not add any while those
// releases are supported. See docs/compatibility.md.)
type Registry struct {
	Schema  int       `json:"schema"`
	Plugins []Listing `json:"plugins"`
	// Revocations are every release the registry has withdrawn, listed or
	// not, from the revocations document beside the catalog.
	Revocations []Revocation `json:"revocations,omitempty"`
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
	Featured       bool         `json:"featured,omitempty"`
	Official       bool         `json:"official,omitempty"`
	Verified       bool         `json:"verified,omitempty"`
	Deprecated     bool         `json:"deprecated,omitempty"`
	Archived       bool         `json:"archived,omitempty"`
	Releases       []Release    `json:"releases"`
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
	// Revoked is set when the registry has withdrawn the release. It stays
	// listed, for the record, but is never installed or updated to.
	Revoked *Revocation `json:"revoked,omitempty"`
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
// A registry without a revocations document (404) has revoked nothing; one
// that cannot be read is an error, so a withdrawn release is never
// installed because its revocation was out of reach.
func (c *RegistryClient) Fetch(ctx context.Context) (Registry, error) {
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return Registry{}, fmt.Errorf("invalid plugin registry URL %q", c.URL)
	}
	body, _, err := c.get(ctx, u.String())
	if err != nil {
		return Registry{}, err
	}
	var catalog Registry
	if err := decodeOne(body, &catalog); err != nil {
		return Registry{}, fmt.Errorf("decode plugin registry: %w", err)
	}
	if catalog.Schema != 1 {
		return Registry{}, fmt.Errorf("unsupported plugin registry schema %d", catalog.Schema)
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
		releases := make(map[string]bool, len(p.Releases))
		for j := range p.Releases {
			r := &p.Releases[j]
			if !versionPattern.MatchString(r.Version) || (strings.TrimPrefix(r.Ref, "v") != strings.TrimPrefix(r.Version, "v")) || len(r.Commit) != 40 || r.PublishedAt.IsZero() || !strings.HasPrefix(strings.ToLower(r.Checksum), "sha256:") || len(r.Checksum) != len("sha256:")+64 || !isHex(strings.TrimPrefix(strings.ToLower(r.Checksum), "sha256:")) {
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
		}
		slices.SortFunc(p.Releases, func(a, b Release) int {
			return compareVersions(b.Version, a.Version)
		})
	}
	slices.SortFunc(catalog.Plugins, func(a, b Listing) int { return strings.Compare(a.ID, b.ID) })
	revoked, err := c.revocations(ctx, u)
	if err != nil {
		return Registry{}, err
	}
	catalog.Revocations = append(catalog.Revocations, revoked...)
	for i := range catalog.Revocations {
		if err := catalog.Revocations[i].validate(); err != nil {
			return Registry{}, err
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

// revocations reads the revocations document beside the catalog at u.
func (c *RegistryClient) revocations(ctx context.Context, catalog *url.URL) ([]Revocation, error) {
	v := *catalog
	dir := path.Dir(v.Path)
	if dir == "." {
		dir = "/"
	}
	v.Path, v.RawPath, v.RawQuery = path.Join(dir, RevocationsFile), "", ""
	body, status, err := c.get(ctx, v.String())
	if status == http.StatusNotFound || status == http.StatusGone {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot tell which plugin releases are revoked: %w", err)
	}
	var doc revocationDocument
	if err := decodeOne(body, &doc); err != nil {
		return nil, fmt.Errorf("decode plugin revocations: %w", err)
	}
	if doc.Schema != 1 {
		return nil, fmt.Errorf("unsupported plugin revocations schema %d", doc.Schema)
	}
	return doc.Releases, nil
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
