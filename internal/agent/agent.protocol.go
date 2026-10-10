package agent

import (
	"context"
	"errors"
	"time"

	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/terminal"
)

type idParams struct {
	ID string `json:"id"`
}

type listParams struct {
	// All lists every terminal process, not only recognised agents.
	All bool `json:"all,omitempty"`
}

// ListResult is agent.list's answer: the agents and their rollups.
type ListResult struct {
	Agents  []Agent `json:"agents"`
	Rollups Rollups `json:"rollups"`
}

type reportParams struct {
	// ID is the agent's pane or process.
	ID        string `json:"id"`
	State     State  `json:"state"`
	TTLMS     int64  `json:"ttl_ms,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Message   string `json:"message,omitempty"`
}

// PromptParams is agent.prompt's request.
type PromptParams struct {
	// ID is the agent's pane or process.
	ID string `json:"id"`
	PromptRequest
	TimeoutMS      int64 `json:"timeout_ms,omitempty"`
	StartTimeoutMS int64 `json:"start_timeout_ms,omitempty"`
	// StallTimeoutMS: 0 is the default (10 minutes), negative never.
	StallTimeoutMS int64 `json:"stall_timeout_ms,omitempty"`
}

// WaitParams is agent.wait's request.
type WaitParams struct {
	ID string `json:"id"`
	WaitRequest
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
	StallMS   int64 `json:"stall_ms,omitempty"`
}

// ReadParams is agent.read's request.
type ReadParams struct {
	ID string `json:"id"`
	terminal.ReadRequest
}

// KeysParams is agent.send_keys's request.
type KeysParams struct {
	ID   string   `json:"id"`
	Keys []string `json:"keys"`
}

func ms(n int64) time.Duration { return time.Duration(n) * time.Millisecond }

// ReloadResult is agent.reload's answer.
type ReloadResult struct {
	Manifests []string `json:"manifests"` // IDs
	Warnings  []string `json:"warnings,omitempty"`
}

// Register exposes the service as agent.*.
func Register(r *protocol.Router, s *Service) {
	m := func(name string, h protocol.Handler) { r.MustRegister(name, h) }

	m("agent.list", protocol.Method(func(ctx context.Context, p listParams) (ListResult, error) {
		list, err := s.List(ctx, p.All)
		if err != nil {
			return ListResult{}, wireError(err)
		}
		if list == nil {
			list = []Agent{}
		}
		return ListResult{Agents: list, Rollups: RollupOf(list)}, nil
	}))
	m("agent.get", protocol.Method(func(ctx context.Context, p idParams) (Agent, error) {
		a, err := s.Get(ctx, p.ID)
		return a, wireError(err)
	}))
	m("agent.explain", protocol.Method(func(ctx context.Context, p idParams) (Explanation, error) {
		ex, err := s.Explain(ctx, p.ID)
		return ex, wireError(err)
	}))
	m("agent.report", protocol.Method(func(ctx context.Context, p reportParams) (Agent, error) {
		a, err := s.Report(ctx, p.ID, Report{
			State:     p.State,
			TTL:       time.Duration(p.TTLMS) * time.Millisecond,
			SessionID: p.SessionID,
			Message:   p.Message,
		})
		return a, wireError(err)
	}))
	m("agent.seen", protocol.Method(func(ctx context.Context, p idParams) (map[string]bool, error) {
		if err := s.Seen(ctx, p.ID); err != nil {
			return nil, wireError(err)
		}
		return map[string]bool{"ok": true}, nil
	}))
	m("agent.prompt", protocol.Method(func(ctx context.Context, p PromptParams) (PromptResult, error) {
		req := p.PromptRequest
		req.Timeout, req.StartTimeout, req.StallTimeout = ms(p.TimeoutMS), ms(p.StartTimeoutMS), ms(p.StallTimeoutMS)
		res, err := s.Prompt(ctx, p.ID, req)
		return res, wireError(err)
	}))
	m("agent.wait", protocol.Method(func(ctx context.Context, p WaitParams) (WaitResult, error) {
		req := p.WaitRequest
		req.Timeout, req.Stall = ms(p.TimeoutMS), ms(p.StallMS)
		res, err := s.Wait(ctx, p.ID, req)
		return res, wireError(err)
	}))
	m("agent.read", protocol.Method(func(ctx context.Context, p ReadParams) ([]string, error) {
		lines, err := s.Read(ctx, p.ID, p.ReadRequest)
		if lines == nil && err == nil {
			lines = []string{}
		}
		return lines, wireError(err)
	}))
	m("agent.send_keys", protocol.Method(func(ctx context.Context, p KeysParams) (map[string]bool, error) {
		if err := s.SendKeys(ctx, p.ID, p.Keys); err != nil {
			return nil, wireError(err)
		}
		return map[string]bool{"ok": true}, nil
	}))
	m("agent.manifests", protocol.Method(func(context.Context, struct{}) ([]*Manifest, error) {
		return s.Manifests(), nil
	}))
	m("agent.reload", protocol.Method(func(context.Context, struct{}) (ReloadResult, error) {
		warnings, err := s.Reload()
		if err != nil {
			return ReloadResult{Warnings: warnings}, err
		}
		res := ReloadResult{Warnings: warnings}
		for _, m := range s.Manifests() {
			res.Manifests = append(res.Manifests, m.ID)
		}
		return res, nil
	}))
}

// wireError gives domain errors their protocol codes.
func wireError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return protocol.NewError(protocol.ErrorCodeNotFound, err)
	case errors.Is(err, ErrInvalid), errors.Is(err, terminal.ErrInvalidRead):
		return protocol.NewError(protocol.ErrorCodeInvalidParams, err)
	case errors.Is(err, ErrBlocked):
		return protocol.NewError(protocol.ErrorCodeConflict, err)
	case errors.Is(err, ErrNotRunning), errors.Is(err, terminal.ErrEnded):
		return protocol.NewError(protocol.ErrorCodeUnavailable, err)
	case errors.Is(err, context.DeadlineExceeded):
		return protocol.NewError(protocol.ErrorCodeTimeout, err)
	}
	return err
}
