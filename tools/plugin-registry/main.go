// Command plugin-registry keeps the plugin registry's schema 2 catalog
// (registry-2.json) and its signatures. It reads, in the registry
// directory:
//
//	official-listings.json    the official plugins' listings (schema 1 fields)
//	official-releases.json    their releases: tags, commits, checksums
//	official-signatures.json  their schema 2 additions: plugin API, signature, provenance
//	publishers.json           publishers, their tiers and signing keys
//	compat-results.json       releases tested against Hive releases (plugin-compat)
//	submissions/*.json        community listings; approved ones with a publisher are listed
//
// Commands:
//
//	go run ./tools/plugin-registry verify                 # what CI runs on every registry change
//	HIVE_PLUGIN_SIGNING_KEY=… go run ./tools/plugin-registry sign   # sign unsigned official releases
//	go run ./tools/plugin-registry build -o registry-2.json [--if-ready]
//	go run ./tools/plugin-registry record-tests results.json
//	HIVE_PLUGIN_SIGNING_KEY=… go run ./tools/plugin-registry public-key
//
// The official signing key is an ed25519 key in tools/release-sign's format
// (`go run ./tools/release-sign keygen plugin-signing.key`), kept as the
// HIVE_PLUGIN_SIGNING_KEY secret; its public half is the "hive" publisher's
// key in publishers.json.
//
// build checks the document it writes exactly as Hive does
// (plugin.ParseCatalog), and refuses to write one in which an official or
// verified release is unsigned: Hive would refuse to install it. With
// --if-ready it then writes nothing and exits 0, so the store sync keeps
// serving schema 1 alone until the official releases are signed.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/plugin"
)

func main() {
	if err := run(os.Args[1:], os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "plugin-registry:", err)
		os.Exit(1)
	}
}

// OfficialPublisher is the publisher of the official plugins.
const OfficialPublisher = "hive"

// errNotReady says official releases are not signed yet.
var errNotReady = errors.New("not ready")

func run(args []string, getenv func(string) string) error {
	if len(args) == 0 {
		return errors.New("usage: plugin-registry verify | sign | build -o <file> [--if-ready] | record-tests <results.json> | public-key  [--dir registry]")
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	dir := fs.String("dir", "registry", "the registry directory")
	out := fs.String("o", "", "where build writes registry-2.json")
	ifReady := fs.Bool("if-ready", false, "build: write nothing (and succeed) while official releases are unsigned")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	r := &registry{dir: *dir, getenv: getenv, now: time.Now}
	switch args[0] {
	case "verify":
		_, err := r.build()
		if errors.Is(err, errNotReady) {
			fmt.Println("plugin-registry:", err, "(registry-2.json is not published until they are)")
			return nil
		}
		if err == nil {
			fmt.Println("plugin-registry: the schema 2 catalog is valid and ready")
		}
		return err
	case "build":
		if *out == "" {
			return errors.New("build needs -o <file>")
		}
		doc, err := r.build()
		if errors.Is(err, errNotReady) && *ifReady {
			fmt.Println("plugin-registry: not writing registry-2.json:", err)
			return nil
		}
		if err != nil {
			return err
		}
		return writeJSON(*out, doc)
	case "sign":
		return r.sign()
	case "record-tests":
		if fs.NArg() != 1 {
			return errors.New("usage: plugin-registry record-tests <results.json>")
		}
		return r.recordTests(fs.Arg(0))
	case "public-key":
		priv, err := signingKey(getenv)
		if err != nil {
			return err
		}
		pub := plugin.PublicKeyOf(priv.Public().(ed25519.PublicKey))
		fmt.Println(pub.String())
		fmt.Println(pub.Fingerprint())
		return nil
	}
	return fmt.Errorf("unknown command %q", args[0])
}

type registry struct {
	dir    string
	getenv func(string) string
	now    func() time.Time
}

func (r *registry) path(name string) string { return filepath.Join(r.dir, name) }

// officialRelease is a release in official-releases.json.
type officialRelease struct {
	Version     string    `json:"version"`
	Ref         string    `json:"ref"`
	Commit      string    `json:"commit"`
	Checksum    string    `json:"checksum"`
	PublishedAt time.Time `json:"published_at"`
}

type officialReleases struct {
	Schema  int `json:"schema"`
	Plugins []struct {
		ID       string            `json:"id"`
		Subdir   string            `json:"subdir"`
		Releases []officialRelease `json:"releases"`
	} `json:"plugins"`
}

// signatureRecord is an official release's schema 2 additions.
type signatureRecord struct {
	ID         string             `json:"id"`
	Version    string             `json:"version"`
	PluginAPI  int                `json:"plugin_api"`
	Signature  string             `json:"signature"`
	Provenance *plugin.Provenance `json:"provenance,omitempty"`
	SignedAt   time.Time          `json:"signed_at"`
}

type signatures struct {
	Schema   int               `json:"schema"`
	Releases []signatureRecord `json:"releases"`
}

type publishers struct {
	Schema     int                `json:"schema"`
	Publishers []plugin.Publisher `json:"publishers"`
}

// compatResult is a release tested against a Hive release.
type compatResult struct {
	ID      string    `json:"id"`
	Version string    `json:"version"`
	Hive    string    `json:"hive"`
	Result  string    `json:"result"`
	At      time.Time `json:"at"`
	Report  string    `json:"report,omitempty"`
}

type compatResults struct {
	Schema  int            `json:"schema"`
	Results []compatResult `json:"results"`
}

func readJSON(path string, v any, optional bool) error {
	data, err := os.ReadFile(path)
	if optional && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// listingAPI is a schema 1 listing's plugin_api ("1") as a number.
func listingAPI(listing map[string]any) int {
	switch v := listing["plugin_api"].(type) {
	case string:
		n, _ := strconv.Atoi(v)
		return n
	case float64:
		return int(v)
	}
	return 0
}

// build assembles the schema 2 catalog and checks it as Hive will. It
// returns errNotReady when an official or verified release is unsigned.
func (r *registry) build() (map[string]any, error) {
	var listings map[string]map[string]any
	if err := readJSON(r.path("official-listings.json"), &listings, false); err != nil {
		return nil, err
	}
	var releases officialReleases
	if err := readJSON(r.path("official-releases.json"), &releases, false); err != nil {
		return nil, err
	}
	var sigs signatures
	if err := readJSON(r.path("official-signatures.json"), &sigs, true); err != nil {
		return nil, err
	}
	var pubs publishers
	if err := readJSON(r.path("publishers.json"), &pubs, false); err != nil {
		return nil, err
	}
	var compat compatResults
	if err := readJSON(r.path("compat-results.json"), &compat, true); err != nil {
		return nil, err
	}
	tested := func(id, version string) []plugin.CompatResult {
		var out []plugin.CompatResult
		for _, c := range compat.Results {
			if c.ID == id && c.Version == version {
				out = append(out, plugin.CompatResult{Hive: c.Hive, Result: c.Result, At: c.At, Report: c.Report})
			}
		}
		return out
	}
	known := map[string]bool{}
	for _, p := range pubs.Publishers {
		known[p.ID] = true
	}
	if !known[OfficialPublisher] {
		return nil, fmt.Errorf("publishers.json has no %q publisher for the official plugins", OfficialPublisher)
	}
	var plugins []map[string]any
	for _, p := range releases.Plugins {
		listing, ok := listings[p.ID]
		if !ok {
			return nil, fmt.Errorf("official release %s has no listing in official-listings.json", p.ID)
		}
		entry := map[string]any{}
		for k, v := range listing {
			if k != "official" && k != "verified" {
				entry[k] = v
			}
		}
		entry["publisher"] = OfficialPublisher
		api := listingAPI(listing)
		var rs []any
		for _, rel := range p.Releases {
			rec := map[string]any{
				"version": rel.Version, "ref": rel.Ref, "commit": rel.Commit, "checksum": rel.Checksum,
				"subdir": p.Subdir, "published_at": rel.PublishedAt.UTC().Format(time.RFC3339), "plugin_api": api,
			}
			if i := slices.IndexFunc(sigs.Releases, func(s signatureRecord) bool { return s.ID == p.ID && s.Version == rel.Version }); i >= 0 {
				s := sigs.Releases[i]
				rec["plugin_api"], rec["signature"] = s.PluginAPI, s.Signature
				if s.Provenance != nil {
					rec["provenance"] = s.Provenance
				}
			}
			if t := tested(p.ID, rel.Version); len(t) > 0 {
				rec["tested"] = t
			}
			rs = append(rs, rec)
		}
		entry["releases"] = rs
		plugins = append(plugins, entry)
	}
	community, err := r.submissions(known, tested)
	if err != nil {
		return nil, err
	}
	plugins = append(plugins, community...)
	doc := map[string]any{"schema": 2, "publishers": pubs.Publishers, "plugins": plugins}
	body, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	catalog, err := plugin.ParseCatalog(body)
	if err != nil {
		return nil, fmt.Errorf("the schema 2 catalog would not be read: %w", err)
	}
	var unsigned []string
	for _, p := range catalog.Plugins {
		for _, rel := range p.Releases {
			if rel.SignatureError != "" {
				return nil, fmt.Errorf("%s %s: %s", p.ID, rel.Version, rel.SignatureError)
			}
			if rel.SignedBy == "" && (p.Tier == plugin.TierOfficial || p.Tier == plugin.TierVerified) {
				unsigned = append(unsigned, p.ID+" "+rel.Version)
			}
		}
	}
	if len(unsigned) > 0 {
		return nil, fmt.Errorf("%w: official or verified releases are unsigned: %s", errNotReady, strings.Join(unsigned, ", "))
	}
	return doc, nil
}

// submissions are the approved community listings that name a publisher.
func (r *registry) submissions(known map[string]bool, tested func(id, version string) []plugin.CompatResult) ([]map[string]any, error) {
	files, _ := filepath.Glob(r.path(filepath.Join("submissions", "*.json")))
	var out []map[string]any
	for _, f := range files {
		if filepath.Base(f) == "schema-v1.json" {
			continue
		}
		var sub struct {
			State   string         `json:"state"`
			Listing map[string]any `json:"listing"`
		}
		if err := readJSON(f, &sub, false); err != nil {
			return nil, err
		}
		publisher, _ := sub.Listing["publisher"].(string)
		if sub.State != "approved" || publisher == "" {
			continue
		}
		if !known[publisher] {
			return nil, fmt.Errorf("%s names publisher %q, which publishers.json does not list", f, publisher)
		}
		entry := map[string]any{}
		for k, v := range sub.Listing {
			if k != "official" && k != "verified" && k != "tier" {
				entry[k] = v
			}
		}
		id, _ := entry["id"].(string)
		releases, _ := entry["releases"].([]any)
		for _, rel := range releases {
			m, ok := rel.(map[string]any)
			if !ok {
				continue
			}
			delete(m, "signed_by")
			delete(m, "signature_error")
			delete(m, "revoked")
			if v, _ := m["version"].(string); v != "" {
				if t := tested(id, v); len(t) > 0 {
					m["tested"] = t
				}
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

func signingKey(getenv func(string) string) (ed25519.PrivateKey, error) {
	raw := strings.TrimSpace(getenv("HIVE_PLUGIN_SIGNING_KEY"))
	if raw == "" {
		return nil, errors.New("set HIVE_PLUGIN_SIGNING_KEY (go run ./tools/release-sign keygen plugin-signing.key)")
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(b) != ed25519.PrivateKeySize {
		return nil, errors.New("HIVE_PLUGIN_SIGNING_KEY is not a base64 ed25519 private key")
	}
	return ed25519.PrivateKey(b), nil
}

// sign signs every official release that has no signature yet, with the
// official key, which must be one of the "hive" publisher's keys.
func (r *registry) sign() error {
	priv, err := signingKey(r.getenv)
	if err != nil {
		return err
	}
	pub := plugin.PublicKeyOf(priv.Public().(ed25519.PublicKey))
	var pubs publishers
	if err := readJSON(r.path("publishers.json"), &pubs, false); err != nil {
		return err
	}
	i := slices.IndexFunc(pubs.Publishers, func(p plugin.Publisher) bool { return p.ID == OfficialPublisher })
	if i < 0 || !slices.ContainsFunc(pubs.Publishers[i].Keys, func(k plugin.PublisherKey) bool {
		parsed, err := plugin.ParsePublicKey(k.Key)
		return err == nil && parsed.Fingerprint() == pub.Fingerprint()
	}) {
		return fmt.Errorf("the signing key %s is not a key of the %q publisher in publishers.json; add it (plugin-registry public-key) first", pub.Fingerprint(), OfficialPublisher)
	}
	var listings map[string]map[string]any
	if err := readJSON(r.path("official-listings.json"), &listings, false); err != nil {
		return err
	}
	var releases officialReleases
	if err := readJSON(r.path("official-releases.json"), &releases, false); err != nil {
		return err
	}
	sigs := signatures{Schema: 1}
	if err := readJSON(r.path("official-signatures.json"), &sigs, true); err != nil {
		return err
	}
	signed := 0
	for _, p := range releases.Plugins {
		api := listingAPI(listings[p.ID])
		for _, rel := range p.Releases {
			if slices.ContainsFunc(sigs.Releases, func(s signatureRecord) bool { return s.ID == p.ID && s.Version == rel.Version }) {
				continue // signatures, like releases, are never rewritten
			}
			release := plugin.Release{Version: rel.Version, Ref: rel.Ref, Commit: rel.Commit, Checksum: rel.Checksum, PluginAPI: api, PublishedAt: rel.PublishedAt}
			sigs.Releases = append(sigs.Releases, signatureRecord{
				ID: p.ID, Version: rel.Version, PluginAPI: api,
				Signature:  plugin.SignStatement(priv, plugin.ReleaseStatement(p.ID, release)),
				Provenance: plugin.ProvenanceFromEnv(r.getenv), SignedAt: r.now().UTC().Truncate(time.Second),
			})
			signed++
			fmt.Printf("signed %s %s with %s\n", p.ID, rel.Version, pub.Fingerprint())
		}
	}
	if signed == 0 {
		fmt.Println("plugin-registry: every official release is signed")
		return nil
	}
	return writeJSON(r.path("official-signatures.json"), sigs)
}

// recordTests adds plugin-compat results (hive plugin test --json output,
// one object or a list) to compat-results.json.
func (r *registry) recordTests(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	type testResult struct {
		Plugin  string    `json:"plugin"`
		Version string    `json:"version"`
		Hive    string    `json:"hive"`
		Result  string    `json:"result"`
		At      time.Time `json:"at"`
		Report  string    `json:"report,omitempty"`
	}
	var list []testResult
	if err := json.Unmarshal(data, &list); err != nil {
		var one testResult
		if err := json.Unmarshal(data, &one); err != nil {
			return fmt.Errorf("%s: want hive plugin test --json results: %w", path, err)
		}
		list = []testResult{one}
	}
	compat := compatResults{Schema: 1}
	if err := readJSON(r.path("compat-results.json"), &compat, true); err != nil {
		return err
	}
	for _, t := range list {
		if t.Plugin == "" || t.Version == "" || t.Hive == "" || (t.Result != "pass" && t.Result != "fail") {
			return fmt.Errorf("%s: an incomplete result: %+v", path, t)
		}
		rec := compatResult{ID: t.Plugin, Version: t.Version, Hive: t.Hive, Result: t.Result, At: t.At.UTC(), Report: t.Report}
		// One result per release and Hive: the latest run replaces it.
		compat.Results = slices.DeleteFunc(compat.Results, func(c compatResult) bool {
			return c.ID == rec.ID && c.Version == rec.Version && c.Hive == rec.Hive
		})
		compat.Results = append(compat.Results, rec)
	}
	slices.SortFunc(compat.Results, func(a, b compatResult) int {
		return strings.Compare(a.ID+" "+a.Version+" "+a.Hive, b.ID+" "+b.Version+" "+b.Hive)
	})
	return writeJSON(r.path("compat-results.json"), compat)
}
