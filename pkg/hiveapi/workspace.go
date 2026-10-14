package hiveapi

import "context"

// PopupOptions configures a terminal process opened in a floating tab view.
type PopupOptions struct {
	TabID     string `json:"tab_id"`
	Spec      Spec   `json:"spec,omitempty"`
	WidthPct  int    `json:"width_pct,omitempty"`
	HeightPct int    `json:"height_pct,omitempty"`
}

// Tabs lists tabs in one environment, or all tabs when environmentID is empty.
func (c *Client) Tabs(ctx context.Context, environmentID string) ([]Tab, error) {
	return c.ws.TabList(ctx, environmentID)
}

// RenameTab changes a tab's user-visible name.
func (c *Client) RenameTab(ctx context.Context, id, name string) (Tab, error) {
	return c.ws.TabRename(ctx, id, name)
}

// Popup launches an interactive child process in a floating Hive pane.
func (c *Client) Popup(ctx context.Context, options PopupOptions) (Pane, error) {
	var result Pane
	err := c.c.Call(ctx, "pane.popup", options, &result)
	return result, err
}

// Processes lists processes in one environment, or all processes when empty.
func (c *Client) Processes(ctx context.Context, environmentID string) ([]Process, error) {
	return c.c.ProcessList(ctx, environmentID)
}

// AgentDirectory returns agents together with their rollups.
func (c *Client) AgentDirectory(ctx context.Context, all bool) (AgentList, error) {
	return c.agents.List(ctx, all)
}
