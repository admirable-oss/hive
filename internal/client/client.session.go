package client

import (
	"context"

	"github.com/admirable-oss/hive/internal/persist"
)

// Sessions is the typed snapshot, restore and clean-up API (session.save,
// session.restore, runtime.gc) over any Client.
type Sessions struct {
	c Client
}

// NewSessions wraps c.
func NewSessions(c Client) Sessions { return Sessions{c: c} }

// Save takes a snapshot of the workspace now.
func (s Sessions) Save(ctx context.Context) (persist.SaveResult, error) {
	var r persist.SaveResult
	err := s.c.Call(ctx, "session.save", struct{}{}, &r)
	return r, err
}

// Restore starts panes whose processes were lost (a reboot) again; nil
// resumeAgents uses the daemon's setting.
func (s Sessions) Restore(ctx context.Context, resumeAgents *bool) (persist.RestoreReport, error) {
	var r persist.RestoreReport
	err := s.c.Call(ctx, "session.restore", persist.RestoreRequest{ResumeAgents: resumeAgents}, &r)
	return r, err
}

// GC removes what is no longer needed (see persist.GCRequest).
func (s Sessions) GC(ctx context.Context, req persist.GCRequest) (persist.GCReport, error) {
	var r persist.GCReport
	err := s.c.Call(ctx, "runtime.gc", req, &r)
	return r, err
}
