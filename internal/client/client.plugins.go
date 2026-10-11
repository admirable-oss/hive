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
