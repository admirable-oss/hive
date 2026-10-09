package pane

import (
	"context"
	"errors"
	"time"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/terminal"
)

type idParams struct {
	ID string `json:"id"`
}

type listParams struct {
	EnvironmentID string `json:"environment_id,omitempty"`
	TabID         string `json:"tab_id,omitempty"`
}

type renameParams struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type tabResizeParams struct {
	ID     string `json:"id"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type focusParams struct {
	ID string `json:"id"`
	// Direction focuses the neighbour of ID on that side instead.
	Direction layout.Direction `json:"direction,omitempty"`
}

type paneResizeParams struct {
	ID        string           `json:"id"`
	Direction layout.Direction `json:"direction"`
	Cells     int              `json:"cells"`
}

type zoomParams struct {
	ID string `json:"id"`
	On *bool  `json:"on,omitempty"` // nil toggles
}

type swapParams struct {
	ID   string `json:"id"`
	With string `json:"with"`
}

type inputParams struct {
	ID   string `json:"id"`
	Data []byte `json:"data"`
}

type textParams struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Paste bool   `json:"paste,omitempty"`
}

type keysParams struct {
	ID   string   `json:"id"`
	Keys []string `json:"keys"`
}

type runParams struct {
	ID      string `json:"id"`
	Command string `json:"command"`
}

type readParams struct {
	ID string `json:"id"`
	terminal.ReadRequest
}

type waitParams struct {
	ID string `json:"id"`
	terminal.WaitRequest
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
}

type exportParams struct {
	Environments []string `json:"environments,omitempty"`
}

// Register exposes the service as tab.*, pane.* and layout.*.
func Register(r *protocol.Router, s *Service) {
	m := func(name string, h protocol.Handler) { r.MustRegister(name, h) }

	m("tab.list", protocol.Method(func(ctx context.Context, p listParams) ([]Tab, error) {
		tabs, err := s.Tabs(ctx, p.EnvironmentID)
		return tabs, wireError(err)
	}))
	m("tab.create", protocol.Method(func(ctx context.Context, p CreateTabRequest) (map[string]any, error) {
		tab, pane, err := s.CreateTab(ctx, p)
		if err != nil {
			return nil, wireError(err)
		}
		return map[string]any{"tab": tab, "pane": pane}, nil
	}))
	m("tab.get", protocol.Method(func(ctx context.Context, p idParams) (Tab, error) {
		t, err := s.Tab(ctx, p.ID)
		return t, wireError(err)
	}))
	m("tab.rename", protocol.Method(func(ctx context.Context, p renameParams) (Tab, error) {
		t, err := s.RenameTab(ctx, p.ID, p.Name)
		return t, wireError(err)
	}))
	m("tab.focus", protocol.Method(func(ctx context.Context, p idParams) (Tab, error) {
		t, err := s.FocusTab(ctx, p.ID)
		return t, wireError(err)
	}))
	m("tab.resize", protocol.Method(func(ctx context.Context, p tabResizeParams) (Tab, error) {
		t, err := s.ResizeTab(ctx, p.ID, p.Width, p.Height)
		return t, wireError(err)
	}))
	m("tab.close", protocol.Method(func(ctx context.Context, p idParams) (protocol.Empty, error) {
		return protocol.Empty{}, wireError(s.CloseTab(ctx, p.ID))
	}))

	m("pane.list", protocol.Method(func(ctx context.Context, p listParams) ([]Pane, error) {
		panes, err := s.Panes(ctx, p.EnvironmentID, p.TabID)
		return panes, wireError(err)
	}))
	m("pane.get", protocol.Method(func(ctx context.Context, p idParams) (Pane, error) {
		pane, err := s.Pane(ctx, p.ID)
		return pane, wireError(err)
	}))
	m("pane.split", protocol.Method(func(ctx context.Context, p SplitRequest) (Pane, error) {
		pane, err := s.Split(ctx, p)
		return pane, wireError(err)
	}))
	m("pane.popup", protocol.Method(func(ctx context.Context, p PopupRequest) (Pane, error) {
		pane, err := s.Popup(ctx, p)
		return pane, wireError(err)
	}))
	m("pane.focus", protocol.Method(func(ctx context.Context, p focusParams) (Pane, error) {
		var (
			pane Pane
			err  error
		)
		if p.Direction != "" {
			pane, err = s.FocusDirection(ctx, p.ID, p.Direction)
		} else {
			pane, err = s.Focus(ctx, p.ID)
		}
		return pane, wireError(err)
	}))
	m("pane.resize", protocol.Method(func(ctx context.Context, p paneResizeParams) (Pane, error) {
		pane, err := s.Resize(ctx, p.ID, p.Direction, p.Cells)
		return pane, wireError(err)
	}))
	m("pane.zoom", protocol.Method(func(ctx context.Context, p zoomParams) (Pane, error) {
		pane, err := s.Zoom(ctx, p.ID, p.On)
		return pane, wireError(err)
	}))
	m("pane.swap", protocol.Method(func(ctx context.Context, p swapParams) (protocol.Empty, error) {
		return protocol.Empty{}, wireError(s.Swap(ctx, p.ID, p.With))
	}))
	m("pane.move", protocol.Method(func(ctx context.Context, p MoveRequest) (Pane, error) {
		pane, err := s.Move(ctx, p)
		return pane, wireError(err)
	}))
	m("pane.rename", protocol.Method(func(ctx context.Context, p renameParams) (Pane, error) {
		pane, err := s.Rename(ctx, p.ID, p.Name)
		return pane, wireError(err)
	}))
	m("pane.close", protocol.Method(func(ctx context.Context, p idParams) (protocol.Empty, error) {
		return protocol.Empty{}, wireError(s.Close(ctx, p.ID))
	}))
	m("pane.input", protocol.Method(func(ctx context.Context, p inputParams) (protocol.Empty, error) {
		return protocol.Empty{}, wireError(s.Input(ctx, p.ID, p.Data))
	}))
	m("pane.send_text", protocol.Method(func(ctx context.Context, p textParams) (protocol.Empty, error) {
		return protocol.Empty{}, wireError(s.SendText(ctx, p.ID, p.Text, p.Paste))
	}))
	m("pane.send_keys", protocol.Method(func(ctx context.Context, p keysParams) (protocol.Empty, error) {
		return protocol.Empty{}, wireError(s.SendKeys(ctx, p.ID, p.Keys))
	}))
	m("pane.run", protocol.Method(func(ctx context.Context, p runParams) (protocol.Empty, error) {
		return protocol.Empty{}, wireError(s.Run(ctx, p.ID, p.Command))
	}))
	m("pane.read", protocol.Method(func(ctx context.Context, p readParams) ([]string, error) {
		lines, err := s.Read(ctx, p.ID, p.ReadRequest)
		return lines, wireError(err)
	}))
	m("pane.wait_output", protocol.Method(func(ctx context.Context, p waitParams) (map[string]string, error) {
		line, err := s.WaitOutput(ctx, p.ID, p.WaitRequest, time.Duration(p.TimeoutMS)*time.Millisecond)
		if err != nil {
			return nil, wireError(err)
		}
		return map[string]string{"line": line}, nil
	}))

	m("layout.export", protocol.Method(func(ctx context.Context, p exportParams) (LayoutSpec, error) {
		sp, err := s.Export(ctx, p.Environments)
		return sp, wireError(err)
	}))
	m("layout.apply", protocol.Method(func(ctx context.Context, p LayoutSpec) (ApplyResult, error) {
		res, err := s.Apply(ctx, p)
		return res, wireError(err)
	}))
}

// wireError gives domain errors their protocol codes.
func wireError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrTabNotFound), errors.Is(err, ErrPaneNotFound),
		errors.Is(err, environment.ErrNotFound), errors.Is(err, layout.ErrNotFound):
		return protocol.NewError(protocol.ErrorCodeNotFound, err)
	case errors.Is(err, ErrInvalid), errors.Is(err, layout.ErrInvalid), errors.Is(err, layout.ErrNoBorder),
		errors.Is(err, terminal.ErrInvalidRead), errors.Is(err, environment.ErrInvalidID),
		errors.Is(err, environment.ErrInvalidRoot), errors.Is(err, environment.ErrInvalidEnv),
		errors.Is(err, process.ErrInvalidCwd), errors.Is(err, process.ErrInvalidEnv),
		errors.Is(err, process.ErrCommandRequired):
		return protocol.NewError(protocol.ErrorCodeInvalidParams, err)
	case errors.Is(err, ErrNotRunning), errors.Is(err, terminal.ErrEnded):
		return protocol.NewError(protocol.ErrorCodeUnavailable, err)
	case errors.Is(err, context.DeadlineExceeded):
		return protocol.NewError(protocol.ErrorCodeTimeout, err)
	}
	return err
}
