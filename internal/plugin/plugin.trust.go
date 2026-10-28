package plugin

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Publisher tiers: who stands behind a plugin. A tier says who published
// it, never what it may do: capabilities and the sandbox decide that.
const (
	// TierOfficial is maintained by Hive's maintainers.
	TierOfficial = "official"
	// TierVerified is from a publisher whose identity a reviewer confirmed.
	TierVerified = "verified"
	// TierCommunity passed the registry's automated checks only.
	TierCommunity = "community"
	// TierUnlisted was installed from git or a directory: nobody but the
	// person who installed it vouches for it.
	TierUnlisted = "unlisted"
)

// Tiers are the publisher tiers a registry may give.
var Tiers = []string{TierOfficial, TierVerified, TierCommunity}

// TierMeaning is what a tier's badge says, for every client.
func TierMeaning(tier string) string {
	switch tier {
	case TierOfficial:
		return "maintained by Hive's maintainers"
	case TierVerified:
		return "publisher identity reviewed"
	case TierCommunity:
		return "automated checks only"
	case TierUnlisted:
		return "installed from source, not from the registry"
	}
	return "not checked against the registry yet (`hive plugin doctor`)"
}

// Provenance is how a release was built, as its publisher recorded it.
// Hive shows it and links the builder's own attestation; the registry's
// submission check verifies the attestation.
type Provenance struct {
	// Builder is "github-actions" or "local".
	Builder     string `json:"builder"`
	Repository  string `json:"repository,omitempty"`
	Workflow    string `json:"workflow,omitempty"`
	Run         string `json:"run,omitempty"`
	Attestation string `json:"attestation,omitempty"`
}

func (p *Provenance) validate() error {
	if p == nil {
		return nil
	}
	if strings.TrimSpace(p.Builder) == "" {
		return fmt.Errorf("provenance has no builder")
	}
	for _, link := range []string{p.Repository, p.Run, p.Attestation} {
		if link != "" && !isHTTPSURL(link) {
			return fmt.Errorf("provenance link %q is not an HTTPS URL", link)
		}
	}
	return nil
}

// String is the provenance in one line.
func (p *Provenance) String() string {
	if p == nil {
		return "not recorded"
	}
	s := p.Builder
	if p.Workflow != "" {
		s += " (" + p.Workflow + ")"
	}
	if p.Run != "" {
		s += " " + p.Run
	}
	return s
}

// Trust is what the registry said of an installed release when it was
// installed, updated or last checked (`hive plugin doctor`): who published
// it, which key signed it, how it was built, and whether it was revoked.
type Trust struct {
	Tier       string      `json:"tier,omitempty"`
	Publisher  string      `json:"publisher,omitempty"`
	SignedBy   string      `json:"signed_by,omitempty"`
	Provenance *Provenance `json:"provenance,omitempty"`
	Revoked    *Revocation `json:"revoked,omitempty"`
	CheckedAt  time.Time   `json:"checked_at,omitzero"`
}

// TrustOf is a listed release's trust, as of now.
func TrustOf(p Listing, r Release) Trust {
	return Trust{Tier: p.Tier, Publisher: p.Publisher, SignedBy: r.SignedBy, Provenance: r.Provenance, Revoked: r.Revoked, CheckedAt: time.Now().UTC()}
}

// Signature policies ([plugins] require_signature).
const (
	// SignaturesOfficialVerified needs official and verified plugins
	// signed, and warns about unsigned community ones.
	SignaturesOfficialVerified = "official-verified"
	// SignaturesAll needs every release signed.
	SignaturesAll = "all"
	// SignaturesNone checks nothing (development registries).
	SignaturesNone = "none"
)

// SignaturePolicies are the values of [plugins] require_signature.
var SignaturePolicies = []string{SignaturesOfficialVerified, SignaturesAll, SignaturesNone}

// CheckSignature decides whether a listed release may be installed under
// a signature policy. A warning is something to tell the person installing
// it; an error refuses the release.
//
// A schema 1 catalog has no signatures at all: under the default policy
// its releases install with a warning, so self-hosted schema 1 registries
// keep working; under "all" they do not install.
func CheckSignature(policy string, schema int, p Listing, r Release) (warning string, err error) {
	if policy == SignaturesNone {
		return "", nil
	}
	if r.SignatureError != "" {
		return "", fmt.Errorf("%s %s: %w: %s", p.ID, r.Version, ErrBadSignature, r.SignatureError)
	}
	if r.SignedBy != "" {
		return "", nil
	}
	switch {
	case policy == SignaturesAll && schema < 2:
		return "", fmt.Errorf("%s %s: the registry serves catalog schema %d, which has no signatures, and [plugins] require_signature = %q", p.ID, r.Version, schema, policy)
	case policy == SignaturesAll:
		return "", fmt.Errorf("%s %s: %w, and [plugins] require_signature = %q", p.ID, r.Version, ErrUnsigned, policy)
	case schema < 2:
		return fmt.Sprintf("%s %s is not signed: the registry serves catalog schema %d, which has no signatures", p.ID, r.Version, schema), nil
	case p.Tier == TierOfficial || p.Tier == TierVerified:
		return "", fmt.Errorf("%s %s: %w, and %s plugins must be ([plugins] require_signature = %q)", p.ID, r.Version, ErrUnsigned, p.Tier, policy)
	}
	return fmt.Sprintf("%s %s is not signed by its publisher (community plugin)", p.ID, r.Version), nil
}

// CheckContinuity refuses an update that changes who stands behind an
// installed plugin: a listing moved to another publisher, or a signed
// release followed by an unsigned one. Reinstalling accepts the change.
func CheckContinuity(policy string, installed Installed, p Listing, r Release) error {
	if policy == SignaturesNone {
		return nil
	}
	if installed.Publisher != "" && p.Publisher != installed.Publisher {
		return fmt.Errorf("%s is now published by %q, not %q as installed; refusing the update (uninstall and install it again to accept the new publisher)", p.ID, cmpEmpty(p.Publisher, "nobody"), installed.Publisher)
	}
	if installed.SignedBy != "" && r.SignedBy == "" {
		return fmt.Errorf("%s %s is not signed, but the installed %s was (by %s); refusing the update", p.ID, r.Version, installed.Version, installed.SignedBy)
	}
	return nil
}

// KeyRevocation distrusts a publisher key: releases it signed that were
// published at or after Since are revoked; earlier ones are not.
type KeyRevocation struct {
	Fingerprint string    `json:"fingerprint"`
	Reason      string    `json:"reason"`
	Advisory    string    `json:"advisory,omitempty"`
	Since       time.Time `json:"since"`
}

func (k KeyRevocation) validate() error {
	if !strings.HasPrefix(k.Fingerprint, "SHA256:") || strings.TrimSpace(k.Reason) == "" || k.Since.IsZero() || (k.Advisory != "" && !isHTTPSURL(k.Advisory)) {
		return fmt.Errorf("invalid revocation of publisher key %q", k.Fingerprint)
	}
	return nil
}

// Publisher is who publishes plugins in a schema 2 registry, with the keys
// their releases are signed with.
type Publisher struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	URL  string         `json:"url,omitempty"`
	Tier string         `json:"tier"`
	Keys []PublisherKey `json:"keys"`
}

// PublisherKey is one of a publisher's signing keys.
type PublisherKey struct {
	Key         string    `json:"key"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	AddedAt     time.Time `json:"added_at,omitzero"`
	parsed      PublicKey
}

func (p *Publisher) validate() error {
	if !idPattern.MatchString(p.ID) || strings.TrimSpace(p.Name) == "" || !slices.Contains(Tiers, p.Tier) || (p.URL != "" && !isHTTPSURL(p.URL)) {
		return fmt.Errorf("invalid publisher %q", p.ID)
	}
	for i := range p.Keys {
		k := &p.Keys[i]
		parsed, err := ParsePublicKey(k.Key)
		if err != nil {
			return fmt.Errorf("publisher %s key %d: %w", p.ID, i, err)
		}
		if k.Fingerprint != "" && k.Fingerprint != parsed.Fingerprint() {
			return fmt.Errorf("publisher %s key %d: fingerprint %s does not match the key (%s)", p.ID, i, k.Fingerprint, parsed.Fingerprint())
		}
		k.Fingerprint, k.parsed = parsed.Fingerprint(), parsed
	}
	return nil
}

func (p Publisher) publicKeys() []PublicKey {
	keys := make([]PublicKey, 0, len(p.Keys))
	for _, k := range p.Keys {
		keys = append(keys, k.parsed)
	}
	return keys
}

// tierOfSchema1 derives a schema 1 listing's tier from its flags.
func tierOfSchema1(p Listing) string {
	switch {
	case p.Official:
		return TierOfficial
	case p.Verified:
		return TierVerified
	}
	return TierCommunity
}

func cmpEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
