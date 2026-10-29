package hiveapi

import (
	"context"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/plugin"
)

// Plugins: what is installed, who stands behind each plugin (its tier,
// publisher and signer), what confines it (its sandbox), and whether its
// release was revoked (`hive plugin list`).
//
//	plugins, err := c.Plugins(ctx)
//	for _, p := range plugins {
//		fmt.Println(p.ID, p.Tier, hiveapi.PluginTierMeaning(p.Tier), p.Sandbox)
//	}
type (
	PluginStatus     = plugin.Status
	PluginTrust      = plugin.Trust
	PluginProvenance = plugin.Provenance
	PluginRevocation = plugin.Revocation
)

// Plugin publisher tiers.
const (
	PluginTierOfficial  = plugin.TierOfficial
	PluginTierVerified  = plugin.TierVerified
	PluginTierCommunity = plugin.TierCommunity
	PluginTierUnlisted  = plugin.TierUnlisted
)

// PluginTierMeaning is what a tier's badge says, as every Hive client
// words it.
func PluginTierMeaning(tier string) string { return plugin.TierMeaning(tier) }

// Plugins lists the plugins the daemon runs.
func (c *Client) Plugins(ctx context.Context) ([]PluginStatus, error) {
	return client.NewPlugins(c.c).List(ctx)
}
