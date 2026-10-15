package pluginsdk

import (
	"context"
	"time"
)

// Capabilities a plugin may hold (plugin API 1).
const (
	CapHiveRead        = "hive.read"
	CapHiveWrite       = "hive.write"
	CapHiveInput       = "hive.input"
	CapHiveProcess     = "hive.process"
	CapHiveAdmin       = "hive.admin"
	CapFilesystemWrite = "filesystem.write"
	CapNetwork         = "network"
	CapProcessExec     = "process.exec"
)

// CardRow is one line of a sidebar card. Tone is "", info, success,
// warning or error.
type CardRow struct {
	Label string `json:"label,omitempty"`
	Text  string `json:"text"`
	Tone  string `json:"tone,omitempty"`
}

// Link is an http(s) link on a card.
type Link struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Card sets what one of the plugin's declared cards shows, about an
// environment (environment cards) or an agent's pane (agent cards). No
// rows and no links clear it. TTL defaults to 30 minutes.
type Card struct {
	Card        string        `json:"card"`
	Environment string        `json:"environment,omitempty"`
	Pane        string        `json:"pane,omitempty"`
	Rows        []CardRow     `json:"rows,omitempty"`
	Links       []Link        `json:"links,omitempty"`
	TTL         time.Duration `json:"ttl,omitempty"`
}

// Status sets one of the plugin's declared tab-bar indicators; no text
// clears it.
type Status struct {
	Indicator string        `json:"indicator"`
	Text      string        `json:"text,omitempty"`
	Tone      string        `json:"tone,omitempty"`
	TTL       time.Duration `json:"ttl,omitempty"`
}

// Discovery is what the hive the plugin runs in offers it.
type Discovery struct {
	API           int      `json:"api"`
	SupportedAPIs []int    `json:"supported_apis"`
	HiveVersion   string   `json:"hive_version"`
	Events        []string `json:"events"`
	Contributions []string `json:"contributions"`
	Isolation     []string `json:"isolation"`
	Plugin        string   `json:"plugin,omitempty"`
	Granted       []string `json:"granted,omitempty"`
	// Methods are every daemon method this plugin may call.
	Methods []string `json:"methods,omitempty"`
}

// SetCard shows (or clears) a card.
func (p *Plugin) SetCard(ctx context.Context, c Card) error {
	return p.Hive.Call(ctx, "plugin.card", c, nil)
}

// SetStatus shows (or clears) a status indicator.
func (p *Plugin) SetStatus(ctx context.Context, s Status) error {
	return p.Hive.Call(ctx, "plugin.status", s, nil)
}

// Ready says the plugin's startup is done (otherwise 2s of uptime counts).
func (p *Plugin) Ready(ctx context.Context) error {
	return p.Hive.Call(ctx, "plugin.ready", struct{}{}, nil)
}

// RequestCapability asks the user for an optional capability the manifest
// lists. It is never granted by asking: the user grants it
// (`hive plugin grant`), and the plugin restarts holding it.
func (p *Plugin) RequestCapability(ctx context.Context, capability, reason string) error {
	return p.Hive.Call(ctx, "plugin.request", map[string]string{"capability": capability, "reason": reason}, nil)
}

// Capabilities describes the hive and what this plugin may do in it.
func (p *Plugin) Capabilities(ctx context.Context) (Discovery, error) {
	var d Discovery
	err := p.Hive.Call(ctx, "plugin.capabilities", struct{}{}, &d)
	return d, err
}
