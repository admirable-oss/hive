package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// DefaultRegistryURL is the public Hive plugin catalog. Set
// HIVE_PLUGIN_REGISTRY_URL to use a self-hosted or development registry.
const DefaultRegistryURL = "https://hive.admir-saheta.com/plugins/registry.json"

const maxRegistryBytes = 4 << 20

// Registry is the versioned catalog document served as JSON.
type Registry struct {
	Schema  int       `json:"schema"`
	Plugins []Listing `json:"plugins"`
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
	Changelog   string    `json:"changelog,omitempty"`
	PublishedAt time.Time `json:"published_at"`
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

// Fetch downloads the catalog and rejects unsupported schemas and conflicting
// or malformed plugin records.
func (c *RegistryClient) Fetch(ctx context.Context) (Registry, error) {
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return Registry{}, fmt.Errorf("invalid plugin registry URL %q", c.URL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Registry{}, err
	}
	req.Header.Set("Accept", "application/json")
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return Registry{}, fmt.Errorf("plugin registry unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Registry{}, fmt.Errorf("plugin registry returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRegistryBytes+1))
	if err != nil {
		return Registry{}, fmt.Errorf("read plugin registry: %w", err)
	}
	if len(body) > maxRegistryBytes {
		return Registry{}, fmt.Errorf("plugin registry exceeds %d bytes", maxRegistryBytes)
	}
	var catalog Registry
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&catalog); err != nil {
		return Registry{}, fmt.Errorf("decode plugin registry: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return Registry{}, fmt.Errorf("decode plugin registry: trailing JSON data")
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
		}
		slices.SortFunc(p.Releases, func(a, b Release) int {
			return compareVersions(b.Version, a.Version)
		})
	}
	slices.SortFunc(catalog.Plugins, func(a, b Listing) int { return strings.Compare(a.ID, b.ID) })
	return catalog, nil
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
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
