package core

import (
	"context"
	"time"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// Plugin is an installed plugin as the Plugins window shows it: who stands
// behind it, what confines it, and whether its release was revoked.
type Plugin struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	// Tier is official, verified, community or unlisted ("" until the
	// registry was checked); TierMeaning says what that means.
	Tier        string `json:"tier"`
	TierMeaning string `json:"tier_meaning"`
	Publisher   string `json:"publisher,omitempty"`
	SignedBy    string `json:"signed_by,omitempty"`
	Provenance  string `json:"provenance,omitempty"`
	// Sandbox is none, sandbox-exec or bwrap.
	Sandbox  string `json:"sandbox"`
	Enabled  bool   `json:"enabled"`
	State    string `json:"state"`
	Problem  string `json:"problem,omitempty"`
	Registry bool   `json:"registry"`
	// Revoked is why the registry withdrew the installed release, and
	// Advisory where to read more; empty when it did not.
	Revoked  string `json:"revoked,omitempty"`
	Advisory string `json:"advisory,omitempty"`
	// CheckedAt is when the registry was last asked (zero: never).
	CheckedAt time.Time `json:"checked_at"`
}

// Plugins lists the plugins the daemon runs.
func (h *Hub) Plugins() ([]Plugin, error) {
	out := []Plugin{}
	err := h.call(func(ctx context.Context, c *hiveapi.Client) error {
		list, err := c.Plugins(ctx)
		for _, p := range list {
			out = append(out, pluginOf(p))
		}
		return err
	})
	return out, err
}

func pluginOf(p hiveapi.PluginStatus) Plugin {
	out := Plugin{
		ID: p.ID, Name: p.ID, Version: p.Version, Tier: p.Tier, TierMeaning: hiveapi.PluginTierMeaning(p.Tier),
		Publisher: p.Publisher, SignedBy: p.SignedBy, Sandbox: p.Sandbox, Enabled: p.Enabled,
		State: p.Health.State, Problem: p.Problem, Registry: p.Registry, CheckedAt: p.CheckedAt,
	}
	if p.Manifest != nil {
		out.Version = p.Manifest.Version
		if p.Manifest.Name != "" {
			out.Name = p.Manifest.Name
		}
	}
	if p.Provenance != nil {
		out.Provenance = p.Provenance.String()
	}
	if p.Revoked != nil {
		out.Revoked, out.Advisory = p.Revoked.Reason, p.Revoked.Advisory
	}
	if out.Sandbox == "" {
		out.Sandbox = "none"
	}
	return out
}
