package plugin

// Plugin forges (plugin API 2): code hosting Hive opens pull requests on
// through a plugin's command, for the hosts the plugin lists. The daemon
// picks a forge for a remote as it always has: configuration first, then
// a plugin forge whose hosts match, then the built-in ones.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// ForgeTimeout bounds one forge operation.
const ForgeTimeout = 2 * time.Minute

// ForgeOpCreate is the operation that opens a pull request.
const ForgeOpCreate = "pr.create"

// ForgeName is how a plugin's forge is named among Hive's forges.
func ForgeName(plugin, forge string) string { return "plugin:" + plugin + "/" + forge }

// HostPatternMatches reports whether a forge's host pattern (a host name,
// or *.domain for its subdomains) matches host.
func HostPatternMatches(pattern, host string) bool {
	pattern, host = strings.ToLower(pattern), strings.ToLower(strings.TrimSuffix(host, "."))
	if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
		return strings.HasSuffix(host, "."+suffix)
	}
	return host == pattern
}

// PluginForge is a forge a loaded plugin provides.
type PluginForge struct {
	Plugin string
	Decl   ForgeDecl
}

// Name is the forge's name among Hive's forges.
func (f PluginForge) Name() string { return ForgeName(f.Plugin, f.Decl.Name) }

// ForgeFor returns the plugin forge serving host: a plugin that can run
// and lists it, an exact host before a *.domain, then by plugin ID.
func (h *Host) ForgeFor(host string) (PluginForge, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var best PluginForge
	bestExact, found := false, false
	for _, id := range slices.Sorted(maps.Keys(h.plugins)) {
		l := h.plugins[id]
		if l.Manifest == nil || l.problem != "" {
			continue
		}
		for _, f := range l.Manifest.Contributes.Forges {
			for _, p := range f.Hosts {
				if !HostPatternMatches(p, host) {
					continue
				}
				exact := !strings.HasPrefix(p, "*.")
				if !found || exact && !bestExact {
					best, bestExact, found = PluginForge{Plugin: id, Decl: f}, exact, true
				}
			}
		}
	}
	return best, found
}

// ForgeResult is what a forge answers for pr.create.
type ForgeResult struct {
	URL    string `json:"url"`
	Number int    `json:"number,omitempty"`
}

// CallForge runs one operation of a plugin's forge: op and the request's
// fields as JSON on stdin, its answer on stdout.
func (h *Host) CallForge(ctx context.Context, f PluginForge, op string, req any) (json.RawMessage, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	in := map[string]any{}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("a forge request is an object: %w", err)
	}
	in["op"], in["forge"] = op, f.Decl.Name
	return h.Invoke(ctx, f.Plugin, ContribForge, f.Decl.Command, in, ForgeTimeout)
}

// checkForgeHosts checks that a forge's hosts are among the hosts a
// plugin that lists them may reach (port 443).
func (m *Manifest) checkForgeHosts(f ForgeDecl) error {
	allowed := m.Permissions.Network.Hosts
	if len(allowed) == 0 {
		return nil // network without a list reaches every host
	}
	for _, h := range f.Hosts {
		ok := HostAllowed(allowed, h, 443)
		if strings.HasPrefix(h, "*.") {
			ok = coversWildcard(allowed, h)
		}
		if !ok {
			return fmt.Errorf("contributes.forges %s: host %s is not in [permissions.network] hosts (add %s:443)", f.Name, h, h)
		}
	}
	return nil
}

// coversWildcard reports whether a network pattern covers every host a
// *.domain forge pattern does: the same wildcard, or one for a parent.
func coversWildcard(allowed []string, wildcard string) bool {
	domain := strings.TrimPrefix(wildcard, "*.")
	for _, a := range allowed {
		h, port, err := ParseHostPattern(a)
		if err != nil || port != 443 {
			continue
		}
		if parent, ok := strings.CutPrefix(h, "*."); ok && (domain == parent || strings.HasSuffix(domain, "."+parent)) {
			return true
		}
	}
	return false
}
