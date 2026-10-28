package client

import (
	"context"

	"github.com/admirable-oss/hive/internal/metadata"
	"github.com/admirable-oss/hive/internal/plugin"
)

// Plugins is the typed plugin and metadata API (plugin.*, metadata.*) over
// any Client.
type Plugins struct {
	c Client
}

// NewPlugins wraps c.
func NewPlugins(c Client) Plugins { return Plugins{c: c} }

// PluginRun names a plugin's action and where the user is.
type PluginRun struct {
	Plugin      string `json:"plugin"`
	Action      string `json:"action"`
	Environment string `json:"environment,omitempty"`
	Tab         string `json:"tab,omitempty"`
	Pane        string `json:"pane,omitempty"`
}

// List returns the plugins the daemon runs.
func (p Plugins) List(ctx context.Context) ([]plugin.Status, error) {
	var r []plugin.Status
	err := p.c.Call(ctx, "plugin.list", struct{}{}, &r)
	return r, err
}

// Reload makes the daemon read the installed plugins again.
func (p Plugins) Reload(ctx context.Context) ([]plugin.Status, error) {
	var r []plugin.Status
	err := p.c.Call(ctx, "plugin.reload", struct{}{}, &r)
	return r, err
}

// ReloadPlugin restarts one plugin (its crash count too).
func (p Plugins) ReloadPlugin(ctx context.Context, id string) ([]plugin.Status, error) {
	var r []plugin.Status
	err := p.c.Call(ctx, "plugin.reload", map[string]string{"plugin": id}, &r)
	return r, err
}

// Cancel stops an action run in progress.
func (p Plugins) Cancel(ctx context.Context, id, run string) error {
	return p.c.Call(ctx, "plugin.cancel", map[string]string{"plugin": id, "run": run}, nil)
}

// Capabilities describes the daemon's plugin API (and, from a plugin's
// own socket, what that plugin holds).
func (p Plugins) Capabilities(ctx context.Context) (plugin.Discovery, error) {
	var r plugin.Discovery
	err := p.c.Call(ctx, "plugin.capabilities", struct{}{}, &r)
	return r, err
}

// Contributions lists what plugins add to the UI, and what it shows now.
func (p Plugins) Contributions(ctx context.Context) (plugin.Contributions, error) {
	var r plugin.Contributions
	err := p.c.Call(ctx, "plugin.contributions", struct{}{}, &r)
	return r, err
}

// Views lists every plugin view, with what it shows.
func (p Plugins) Views(ctx context.Context) ([]plugin.ViewContent, error) {
	var r []plugin.ViewContent
	err := p.c.Call(ctx, "plugin.views", struct{}{}, &r)
	return r, err
}

// SetView sets a plugin's view (a plugin, on its own socket).
func (p Plugins) SetView(ctx context.Context, u plugin.ViewUpdate) (plugin.ViewContent, error) {
	var r plugin.ViewContent
	err := p.c.Call(ctx, "plugin.view.set", u, &r)
	return r, err
}

// ClearView clears a plugin's view (a plugin, on its own socket).
func (p Plugins) ClearView(ctx context.Context, pluginID, view string) (plugin.ViewContent, error) {
	var r plugin.ViewContent
	err := p.c.Call(ctx, "plugin.view.clear", plugin.ViewUpdate{Plugin: pluginID, View: view}, &r)
	return r, err
}

// ClickView runs the action of a view's button.
func (p Plugins) ClickView(ctx context.Context, k plugin.ViewClick) (plugin.Result, error) {
	var r plugin.Result
	err := p.c.Call(ctx, "plugin.view.click", k, &r)
	return r, err
}

// SetCard shows or clears a plugin's card.
func (p Plugins) SetCard(ctx context.Context, u plugin.CardUpdate) (plugin.CardContent, error) {
	var r plugin.CardContent
	err := p.c.Call(ctx, "plugin.card", u, &r)
	return r, err
}

// SetStatus shows or clears a plugin's status indicator.
func (p Plugins) SetStatus(ctx context.Context, u plugin.StatusUpdate) (plugin.StatusContent, error) {
	var r plugin.StatusContent
	err := p.c.Call(ctx, "plugin.status", u, &r)
	return r, err
}

// Ready says a plugin's startup is done.
func (p Plugins) Ready(ctx context.Context, id string) error {
	return p.c.Call(ctx, "plugin.ready", map[string]string{"plugin": id}, nil)
}

// Request asks the user for an optional capability.
func (p Plugins) Request(ctx context.Context, id, capability, reason string) (plugin.PermissionRequest, error) {
	var r plugin.PermissionRequest
	err := p.c.Call(ctx, "plugin.request", map[string]string{"plugin": id, "capability": capability, "reason": reason}, &r)
	return r, err
}

// Config reads a plugin's settings with their schema.
func (p Plugins) Config(ctx context.Context, id string) (plugin.ConfigView, error) {
	var r plugin.ConfigView
	err := p.c.Call(ctx, "plugin.config", map[string]string{"plugin": id}, &r)
	return r, err
}

// SetConfig changes a plugin's settings (and restarts it on them).
func (p Plugins) SetConfig(ctx context.Context, id string, set map[string]any, unset []string) (plugin.ConfigView, error) {
	var r plugin.ConfigView
	err := p.c.Call(ctx, "plugin.config.set", map[string]any{"plugin": id, "set": set, "unset": unset}, &r)
	return r, err
}

// Run runs a plugin's action and waits for it.
func (p Plugins) Run(ctx context.Context, req PluginRun) (plugin.Result, error) {
	var r plugin.Result
	err := p.c.Call(ctx, "plugin.run", req, &r)
	return r, err
}

// ReportMetadata sets tokens and a title on a pane or environment.
func (p Plugins) ReportMetadata(ctx context.Context, rep metadata.Report) (metadata.Entry, error) {
	var r metadata.Entry
	err := p.c.Call(ctx, "metadata.report", rep, &r)
	return r, err
}

// Metadata lists every pane's and environment's tokens and titles.
func (p Plugins) Metadata(ctx context.Context) ([]metadata.Entry, error) {
	var r []metadata.Entry
	err := p.c.Call(ctx, "metadata.list", struct{}{}, &r)
	return r, err
}
