package hiveapi

import (
	"context"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/vt"
)

// Terminal and layout types, for programs that show agents' screens and
// arrange their panes (the desktop app, say).
type (
	// Frame is a screen update: a keyframe, or the lines that changed.
	Frame = vt.Frame
	// TerminalScreen applies frames (Apply) and holds the screen they make.
	TerminalScreen = vt.Screen
	Cell           = vt.Cell
	CellStyle      = vt.Style
	Color          = vt.Color
	Cursor         = vt.Cursor
	CursorShape    = vt.CursorShape
	Modes          = vt.Modes
	LineUpdate     = vt.LineUpdate
	// FrameStream is an open view of a terminal; see Client.Frames.
	FrameStream = client.FrameStream
	// Status is the daemon's own state: version, API level, PID.
	Status = client.Status
	// Direction is left, right, up or down.
	Direction = layout.Direction
	// Rect is a pane's place in its tab, in cells.
	Rect             = layout.Rect
	SplitRequest     = pane.SplitRequest
	CreateTabRequest = pane.CreateTabRequest
	TabCreated       = client.TabCreated
)

// APILevel is the daemon API level this SDK speaks: a daemon whose
// Status.APILevel is lower lacks some of its methods.
const APILevel = protocol.APILevel

// Directions.
const (
	Left  = layout.Left
	Right = layout.Right
	Up    = layout.Up
	Down  = layout.Down
)

// Terminal modes a viewer needs to encode keys and pastes.
const (
	ModeAppCursorKeys  = vt.ModeAppCursorKeys
	ModeBracketedPaste = vt.ModeBracketedPaste
	ModeAltScreen      = vt.ModeAltScreen
)

// Status reads the daemon's version, API level and PID.
func (c *Client) Status(ctx context.Context) (Status, error) { return c.c.Status(ctx) }

// Frames opens a view of a terminal process sized width × height (zero:
// its own size). Next returns frames: a keyframe first, then the lines that
// changed, paced to what the viewer can take. Writing to the stream types
// into the terminal and makes this view the one whose size it follows;
// Resize reports a new size. Close ends the view; the process keeps
// running.
func (c *Client) Frames(ctx context.Context, processID string, width, height int) (*FrameStream, error) {
	return c.c.TerminalFrames(ctx, client.ViewRequest{ProcessID: processID, Width: uint16(width), Height: uint16(height)})
}

// Input types raw bytes into a terminal process (keys as a terminal sends
// them: "\r" is Enter).
func (c *Client) Input(ctx context.Context, processID string, data []byte) error {
	return c.c.TerminalInput(ctx, processID, data)
}

// SendText types text into a pane; with paste, as a bracketed paste when
// the program asked for those.
func (c *Client) SendText(ctx context.Context, paneID, text string, paste bool) error {
	return c.ws.PaneSendText(ctx, paneID, text, paste)
}

// CreateTab opens a tab in an environment, with one pane.
func (c *Client) CreateTab(ctx context.Context, req CreateTabRequest) (TabCreated, error) {
	return c.ws.TabCreate(ctx, req)
}

// FocusTab makes a tab its environment's active one.
func (c *Client) FocusTab(ctx context.Context, id string) (Tab, error) { return c.ws.TabFocus(ctx, id) }

// ResizeTab sets a tab's area in cells; its panes' sizes follow.
func (c *Client) ResizeTab(ctx context.Context, id string, width, height int) (Tab, error) {
	return c.ws.TabResize(ctx, id, width, height)
}

// CloseTab closes a tab and stops its panes' processes.
func (c *Client) CloseTab(ctx context.Context, id string) error { return c.ws.TabClose(ctx, id) }

// SplitPane splits a pane, starting a new process in the new half.
func (c *Client) SplitPane(ctx context.Context, req SplitRequest) (Pane, error) {
	return c.ws.PaneSplit(ctx, req)
}

// FocusPane makes a pane the one that gets input (and its tab active);
// with a direction, the pane next to it that way.
func (c *Client) FocusPane(ctx context.Context, id string, d Direction) (Pane, error) {
	return c.ws.PaneFocus(ctx, id, d)
}

// ResizePane moves a pane's border that way by cells.
func (c *Client) ResizePane(ctx context.Context, id string, d Direction, cells int) (Pane, error) {
	return c.ws.PaneResize(ctx, id, d, cells)
}

// ZoomPane makes a pane fill its tab (on), or not; nil toggles.
func (c *Client) ZoomPane(ctx context.Context, id string, on *bool) (Pane, error) {
	return c.ws.PaneZoom(ctx, id, on)
}

// RenamePane renames a pane.
func (c *Client) RenamePane(ctx context.Context, id, name string) (Pane, error) {
	return c.ws.PaneRename(ctx, id, name)
}

// ClosePane closes a pane and stops its process.
func (c *Client) ClosePane(ctx context.Context, id string) error { return c.ws.PaneClose(ctx, id) }
