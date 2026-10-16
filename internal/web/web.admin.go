package web

import (
	"context"
	"errors"

	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/protocol"
)

// The admin socket: what `hive web` commands ask a running server. Only the
// user who runs it can connect (0600, peer-checked).

// Shared is a new grant and its link, shown only now.
type Shared struct {
	Grant Grant  `json:"grant"`
	Link  string `json:"link"`
}

// GrantList is web.grants.
type GrantList struct {
	Grants   []Grant   `json:"grants"`
	Sessions []Session `json:"sessions"`
}

// Revoked says what a revoke ended.
type Revoked struct {
	Grant    Grant    `json:"grant"`
	Sessions []string `json:"sessions"`
}

type idParams struct {
	ID string `json:"id"`
}

type auditParams struct {
	Grant string `json:"grant,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

func (s *Server) registerAdmin(r *protocol.Router) {
	r.MustRegister("web.status", protocol.Method(func(context.Context, protocol.Empty) (Status, error) {
		return s.Status(), nil
	}))
	r.MustRegister("web.share", protocol.Method(func(_ context.Context, req ShareRequest) (Shared, error) {
		g, link, err := s.Share(req)
		if err != nil {
			return Shared{}, adminError(err)
		}
		return Shared{Grant: g, Link: link}, nil
	}))
	r.MustRegister("web.grants", protocol.Method(func(context.Context, protocol.Empty) (GrantList, error) {
		gs, xs, err := s.store.Grants()
		return GrantList{Grants: gs, Sessions: xs}, err
	}))
	r.MustRegister("web.revoke", protocol.Method(func(_ context.Context, p idParams) (Revoked, error) {
		g, ended, err := s.Revoke(p.ID)
		if err != nil {
			return Revoked{}, adminError(err)
		}
		if ended == nil {
			ended = []string{}
		}
		return Revoked{Grant: g, Sessions: ended}, nil
	}))
	r.MustRegister("web.audit", protocol.Method(func(_ context.Context, p auditParams) ([]observer.AuditEntry, error) {
		entries, err := s.audit.Read(p.Limit, p.Grant)
		if entries == nil {
			entries = []observer.AuditEntry{}
		}
		return entries, err
	}))
	r.MustRegister("web.reload", protocol.Method(func(context.Context, protocol.Empty) (Status, error) {
		if err := s.Reload(); err != nil {
			return Status{}, err
		}
		return s.Status(), nil
	}))
}

func adminError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return protocol.NewError(protocol.ErrorCodeNotFound, err)
	case errors.Is(err, ErrInvalid), errors.Is(err, observer.ErrInvalid):
		return protocol.NewError(protocol.ErrorCodeInvalidParams, err)
	}
	return err
}
