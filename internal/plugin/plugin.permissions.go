package plugin

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

func apiList() string {
	parts := make([]string, len(SupportedAPIs))
	for i, v := range SupportedAPIs {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ", ")
}

// Legacy reports a manifest from before versioned APIs: it holds every
// capability and contributes nothing.
func (m *Manifest) Legacy() bool { return m.API == 0 }

// EffectiveAPI is the API version the plugin runs under.
func (m *Manifest) EffectiveAPI() int {
	if m.API == 0 {
		return 1
	}
	return m.API
}

// RequiredCapabilities are the capabilities the plugin cannot run without
// (every capability, for a legacy plugin).
func (m *Manifest) RequiredCapabilities() []string {
	if m.Legacy() {
		return capabilityNames(CapabilityList)
	}
	if m.API >= 2 && m.Isolation == IsolationNone && !slices.Contains(m.Permissions.Required, CapUnconfined) {
		return append(slices.Clone(m.Permissions.Required), CapUnconfined)
	}
	return m.Permissions.Required
}

// OptionalCapabilities are those it can use when granted. Every plugin API
// 2 plugin may be granted unconfined, for a system with no sandbox.
func (m *Manifest) OptionalCapabilities() []string {
	if m.Legacy() {
		return nil
	}
	if m.API >= 2 && !slices.Contains(m.RequiredCapabilities(), CapUnconfined) && !slices.Contains(m.Permissions.Optional, CapUnconfined) {
		return append(slices.Clone(m.Permissions.Optional), CapUnconfined)
	}
	return m.Permissions.Optional
}

// Declared lists every capability the manifest mentions.
func (m *Manifest) Declared() []string {
	return sortedUnique(append(slices.Clone(m.RequiredCapabilities()), m.OptionalCapabilities()...))
}

func sortedUnique(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

var contribID = idPattern

// checkSurfaces checks the API 1 sections: permissions, contributions,
// isolation and event concurrency.
func (m *Manifest) checkSurfaces() error {
	if m.Legacy() {
		switch {
		case len(m.Permissions.Required)+len(m.Permissions.Optional) > 0:
			return errors.New("[permissions] needs api = 1")
		case m.hasContributions():
			return errors.New("[contributes] needs api = 1")
		}
	}
	seen := map[string]bool{}
	api := m.EffectiveAPI()
	for _, list := range [][]string{m.Permissions.Required, m.Permissions.Optional} {
		for _, c := range list {
			if !KnownCapabilityFor(api, c) {
				if KnownCapability(c) {
					return fmt.Errorf("permissions: %s needs api = 2", c)
				}
				return fmt.Errorf("permissions: unknown capability %q (have %s)", c, strings.Join(capabilityNames(CapabilitiesFor(api)), ", "))
			}
			if seen[c] {
				return fmt.Errorf("permissions: %s is listed twice", c)
			}
			seen[c] = true
		}
	}
	if err := m.checkScopes(seen); err != nil {
		return err
	}
	for c, reason := range m.Permissions.Reasons {
		if !seen[c] {
			return fmt.Errorf("permissions.reasons: %s is not requested", c)
		}
		if utf8.RuneCountInString(reason) > 200 {
			return fmt.Errorf("permissions.reasons.%s: at most 200 characters", c)
		}
	}
	switch {
	case m.Isolation == "" || m.Isolation == "strict":
		if m.IsolationReason != "" {
			return errors.New("isolation_reason: only with isolation = \"none\"")
		}
	case m.Isolation == IsolationNone && api >= 2:
		if strings.TrimSpace(m.IsolationReason) == "" {
			return errors.New("isolation = \"none\" needs isolation_reason: why it cannot run sandboxed (shown when you install it)")
		}
		if utf8.RuneCountInString(m.IsolationReason) > 200 {
			return errors.New("isolation_reason: at most 200 characters")
		}
	case m.Isolation == IsolationNone:
		return errors.New("isolation = \"none\" needs api = 2")
	default:
		return fmt.Errorf("isolation %q: want strict, none (api 2) or nothing", m.Isolation)
	}
	if m.EventConcurrency < 0 || m.EventConcurrency > 16 {
		return errors.New("event_concurrency: 1 to 16")
	}
	c := &m.Contributes
	switch {
	case len(c.Cards) > MaxCards:
		return fmt.Errorf("contributes.cards: at most %d", MaxCards)
	case len(c.Status) > MaxStatus:
		return fmt.Errorf("contributes.status: at most %d", MaxStatus)
	case len(c.Actions) > MaxContextActions:
		return fmt.Errorf("contributes.actions: at most %d", MaxContextActions)
	case len(c.Palette) > MaxPalette:
		return fmt.Errorf("contributes.palette: at most %d", MaxPalette)
	case len(c.Links) > MaxLinks:
		return fmt.Errorf("contributes.links: at most %d", MaxLinks)
	}
	ids := map[string]bool{}
	checkID := func(kind, id, title string) error {
		switch {
		case !contribID.MatchString(id):
			return fmt.Errorf("contributes.%s: id %q: lower-case letters, digits and dashes", kind, id)
		case ids[kind+"/"+id]:
			return fmt.Errorf("contributes.%s: id %q twice", kind, id)
		case title == "" || utf8.RuneCountInString(title) > MaxTitle:
			return fmt.Errorf("contributes.%s %s: title of 1 to %d characters", kind, id, MaxTitle)
		}
		ids[kind+"/"+id] = true
		return nil
	}
	for i := range c.Cards {
		card := &c.Cards[i]
		if err := checkID("cards", card.ID, card.Title); err != nil {
			return err
		}
		switch card.Scope {
		case "":
			card.Scope = "environment"
		case "environment", "agent":
		default:
			return fmt.Errorf("contributes.cards %s: scope %q (want environment or agent)", card.ID, card.Scope)
		}
	}
	for _, s := range c.Status {
		if err := checkID("status", s.ID, s.Title); err != nil {
			return err
		}
	}
	for i := range c.Actions {
		a := &c.Actions[i]
		act, ok := m.Action(a.Action)
		if !ok {
			return fmt.Errorf("contributes.actions[%d]: no action %q", i, a.Action)
		}
		if a.Title == "" {
			a.Title = act.Title
		}
		if utf8.RuneCountInString(a.Title) > MaxTitle {
			return fmt.Errorf("contributes.actions[%d]: title of at most %d characters", i, MaxTitle)
		}
		for _, ctx := range a.Contexts {
			if !slices.Contains(ActionContexts, ctx) {
				return fmt.Errorf("contributes.actions[%d]: context %q (want %s)", i, ctx, strings.Join(ActionContexts, ", "))
			}
		}
		if len(a.Contexts) == 0 {
			a.Contexts = slices.Clone(ActionContexts)
		}
	}
	for i := range c.Palette {
		p := &c.Palette[i]
		act, ok := m.Action(p.Action)
		if !ok {
			return fmt.Errorf("contributes.palette[%d]: no action %q", i, p.Action)
		}
		if p.Title == "" {
			p.Title = act.Title
		}
		if utf8.RuneCountInString(p.Title) > MaxTitle {
			return fmt.Errorf("contributes.palette[%d]: title of at most %d characters", i, MaxTitle)
		}
	}
	for _, l := range c.Links {
		if err := checkID("links", l.ID, l.Title); err != nil {
			return err
		}
		if !SafeURL(l.URL) {
			return fmt.Errorf("contributes.links %s: url must be http or https", l.ID)
		}
	}
	return nil
}

func (m *Manifest) hasContributions() bool {
	c := m.Contributes
	return len(c.Cards)+len(c.Status)+len(c.Actions)+len(c.Palette)+len(c.Links) > 0
}

// SafeURL reports an absolute http(s) URL of sensible length: the only
// links the UI opens for a plugin.
func SafeURL(s string) bool {
	if len(s) > 2048 {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// Policy is the user's rule for every plugin's capabilities ([plugins] in
// the config): Allow grants optional capabilities without asking, Deny
// withholds capabilities whatever the plugin or the user's grants say.
type Policy struct {
	Allow []string
	Deny  []string
}

// Grant is what a plugin may do, decided from its manifest, the user's
// grants and the policy.
type Grant struct {
	// Effective are the capabilities it holds.
	Effective []string `json:"effective"`
	// Missing are required capabilities it lacks: it does not run until
	// they are granted (or no longer denied).
	Missing []string `json:"missing,omitempty"`
	// Denied are capabilities the policy withholds.
	Denied []string `json:"denied,omitempty"`
	// Ungranted are optional capabilities it could ask for.
	Ungranted []string `json:"ungranted,omitempty"`
}

// Decide works out what a plugin holds. granted is what the user approved
// (required capabilities at install, optional ones with `hive plugin
// grant`). A legacy plugin needs no approval: it holds what it always did,
// minus what the policy denies (and then it cannot run).
func Decide(m *Manifest, granted []string, p Policy) Grant {
	var g Grant
	has := func(c string) bool {
		return m.Legacy() || slices.Contains(granted, c)
	}
	for _, c := range m.RequiredCapabilities() {
		switch {
		case slices.Contains(p.Deny, c):
			g.Denied = append(g.Denied, c)
			g.Missing = append(g.Missing, c)
		case has(c) || slices.Contains(p.Allow, c):
			g.Effective = append(g.Effective, c)
		default:
			g.Missing = append(g.Missing, c)
		}
	}
	for _, c := range m.OptionalCapabilities() {
		switch {
		case slices.Contains(p.Deny, c):
			g.Denied = append(g.Denied, c)
		case has(c) || slices.Contains(p.Allow, c):
			g.Effective = append(g.Effective, c)
		default:
			g.Ungranted = append(g.Ungranted, c)
		}
	}
	g.Effective = sortedUnique(g.Effective)
	return g
}

// Problem says why a plugin with this grant cannot run ("" when it can).
func (g Grant) Problem(id string) string {
	if len(g.Missing) == 0 {
		return ""
	}
	var denied, ungranted []string
	for _, c := range g.Missing {
		if slices.Contains(g.Denied, c) {
			denied = append(denied, c)
		} else {
			ungranted = append(ungranted, c)
		}
	}
	var parts []string
	if len(denied) > 0 {
		parts = append(parts, fmt.Sprintf("needs %s, denied by [plugins] deny", strings.Join(denied, ", ")))
	}
	if len(ungranted) > 0 {
		parts = append(parts, fmt.Sprintf("needs %s, not granted (`hive plugin grant %s %s`)", strings.Join(ungranted, ", "), id, strings.Join(ungranted, " ")))
	}
	return strings.Join(parts, "; ")
}

// PermissionChange is how a new release's requests differ from the
// installed one's.
type PermissionChange struct {
	AddedRequired   []string `json:"added_required,omitempty"`
	AddedOptional   []string `json:"added_optional,omitempty"`
	Removed         []string `json:"removed,omitempty"`
	BecameRequired  []string `json:"became_required,omitempty"`
	ContributionsUp bool     `json:"contributions_changed,omitempty"`
}

// Empty reports no change worth asking about.
func (c PermissionChange) Empty() bool {
	return len(c.AddedRequired)+len(c.AddedOptional)+len(c.Removed)+len(c.BecameRequired) == 0 && !c.ContributionsUp
}

// ComparePermissions compares an installed manifest (old) with a new one.
func ComparePermissions(old, next *Manifest) PermissionChange {
	var c PermissionChange
	oldReq, oldOpt := old.RequiredCapabilities(), old.OptionalCapabilities()
	for _, r := range next.RequiredCapabilities() {
		switch {
		case slices.Contains(oldOpt, r):
			c.BecameRequired = append(c.BecameRequired, r)
		case !slices.Contains(oldReq, r):
			c.AddedRequired = append(c.AddedRequired, r)
		}
	}
	for _, o := range next.OptionalCapabilities() {
		if !slices.Contains(oldReq, o) && !slices.Contains(oldOpt, o) {
			c.AddedOptional = append(c.AddedOptional, o)
		}
	}
	nextAll := next.Declared()
	for _, d := range old.Declared() {
		if !slices.Contains(nextAll, d) {
			c.Removed = append(c.Removed, d)
		}
	}
	c.ContributionsUp = !sameContributions(old.Contributes, next.Contributes)
	return c
}

func sameContributions(a, b Contributes) bool {
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// String describes the change for the user.
func (c PermissionChange) String() string {
	var b strings.Builder
	for _, x := range c.AddedRequired {
		fmt.Fprintf(&b, "  + %s (new, required): %s\n", x, describeCapability(x))
	}
	for _, x := range c.BecameRequired {
		fmt.Fprintf(&b, "  + %s (was optional, now required): %s\n", x, describeCapability(x))
	}
	for _, x := range c.AddedOptional {
		fmt.Fprintf(&b, "  + %s (new, optional; not granted): %s\n", x, describeCapability(x))
	}
	for _, x := range c.Removed {
		fmt.Fprintf(&b, "  - %s (no longer requested)\n", x)
	}
	if c.ContributionsUp {
		b.WriteString("  ~ its UI contributions changed\n")
	}
	return b.String()
}

func describeCapability(name string) string {
	for _, c := range append(slices.Clone(CapabilityListV2), CapabilityList...) {
		if c.Name == name {
			return c.Description
		}
	}
	return name
}

// Approve returns the grants after the user accepts next: every required
// capability, and optional ones previously granted that it still asks for.
func Approve(next *Manifest, granted []string) []string {
	out := slices.Clone(next.RequiredCapabilities())
	for _, g := range granted {
		// Running unconfined is approved for one release: an update asks
		// again (`hive plugin grant <id> unconfined`).
		if g == CapUnconfined {
			continue
		}
		if slices.Contains(next.OptionalCapabilities(), g) {
			out = append(out, g)
		}
	}
	if next.Legacy() {
		return nil
	}
	return sortedUnique(out)
}

// IsolationNone is a plugin API 2 manifest's isolation = "none": run
// outside the sandbox, once approved.
const IsolationNone = "none"

// Bounds on a manifest's scopes.
const (
	MaxNetworkHosts = 32
	MaxExecPrograms = 32
)

// checkScopes checks [permissions.network] and [permissions.process]:
// plugin API 2 only, each for a capability it asks for.
func (m *Manifest) checkScopes(requested map[string]bool) error {
	hosts, exec := m.Permissions.Network.Hosts, m.Permissions.Process.Exec
	if len(hosts)+len(exec) == 0 {
		return nil
	}
	if m.EffectiveAPI() < 2 {
		return errors.New("[permissions.network] and [permissions.process] need api = 2")
	}
	if len(hosts) > 0 && !requested[CapNetwork] {
		return errors.New("permissions.network.hosts: request network too")
	}
	if len(exec) > 0 && !requested[CapProcessExec] {
		return errors.New("permissions.process.exec: request process.exec too")
	}
	if len(hosts) > MaxNetworkHosts {
		return fmt.Errorf("permissions.network.hosts: at most %d", MaxNetworkHosts)
	}
	if len(exec) > MaxExecPrograms {
		return fmt.Errorf("permissions.process.exec: at most %d", MaxExecPrograms)
	}
	for _, h := range hosts {
		if _, _, err := ParseHostPattern(h); err != nil {
			return fmt.Errorf("permissions.network.hosts: %w", err)
		}
	}
	for _, p := range exec {
		switch {
		case p == "" || strings.ContainsAny(p, "\x00\n"):
			return fmt.Errorf("permissions.process.exec: invalid program %q", p)
		case strings.ContainsRune(p, '/') && !strings.HasPrefix(p, "/"):
			return fmt.Errorf("permissions.process.exec: %q: a program name found on PATH, or an absolute path", p)
		}
	}
	return nil
}

// ParseHostPattern reads a host:port a plugin may reach: a host name, an
// IP address, or *.domain for its subdomains; a port from 1 to 65535.
func ParseHostPattern(s string) (host string, port int, err error) {
	h, p, ok := strings.Cut(s, ":")
	if strings.HasPrefix(s, "[") { // [IPv6]:port
		end := strings.Index(s, "]:")
		if end < 0 {
			return "", 0, fmt.Errorf("%q: want host:port", s)
		}
		h, p, ok = s[1:end], s[end+2:], true
	}
	if !ok || h == "" {
		return "", 0, fmt.Errorf("%q: want host:port", s)
	}
	n, perr := strconv.Atoi(p)
	if perr != nil || n < 1 || n > 65535 {
		return "", 0, fmt.Errorf("%q: the port must be 1 to 65535", s)
	}
	name := strings.TrimPrefix(h, "*.")
	if strings.Contains(name, "*") || len(name) > 253 || !validHostName(name) {
		return "", 0, fmt.Errorf("%q: not a host name (use *.domain for subdomains)", s)
	}
	return strings.ToLower(h), n, nil
}

func validHostName(h string) bool {
	if h == "" {
		return false
	}
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.', r == ':':
		default:
			return false
		}
	}
	return true
}

// HostAllowed reports whether host:port matches one of patterns.
func HostAllowed(patterns []string, host string, port int) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, p := range patterns {
		h, n, err := ParseHostPattern(p)
		if err != nil || n != port {
			continue
		}
		if suffix, ok := strings.CutPrefix(h, "*."); ok {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
		} else if host == h {
			return true
		}
	}
	return false
}
