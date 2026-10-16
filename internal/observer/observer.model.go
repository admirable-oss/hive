// Package observer is read-only access to a daemon for programs that watch
// Hive without driving it: dashboards, notifiers, the web and mobile
// observers.
//
// An observer holds a credential (see Store) that names what it may see, its
// scope, and until when. It connects to the daemon's observer socket, not
// the control socket, and authenticates once per connection; every method
// there only reads. What it receives are views (Environment, Pane, Agent, …)
// built for observers: they leave out what could carry secrets (variables,
// command lines, agents' own session IDs). Events reach it through a bounded
// history with sequence numbers, so a client that falls behind or reconnects
// either catches up or is told to resynchronise from a snapshot.
//
// This is access control in the API, not isolation in the operating system:
// any process running as the daemon's user can still open its control
// socket. An observer held to its token runs elsewhere and reaches the
// daemon over SSH with a key restricted to `hive proxy --observer`.
package observer

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Scope is what a credential may observe: every environment of the
// daemon's session (All), or the listed environments and panes. An
// environment includes its tabs, panes, agents and processes; a pane alone
// includes its process and agent but not its tab or environment.
type Scope struct {
	All          bool     `json:"all,omitempty"`
	Environments []string `json:"environments,omitempty"`
	Panes        []string `json:"panes,omitempty"`
}

// ErrInvalid wraps a bad credential request.
var ErrInvalid = errors.New("observer: invalid")

// Validate rejects a scope that names nothing, or All together with a list.
func (s Scope) Validate() error {
	switch {
	case s.All && (len(s.Environments) > 0 || len(s.Panes) > 0):
		return fmt.Errorf("%w: a scope is either all or a list of environments and panes", ErrInvalid)
	case !s.All && len(s.Environments) == 0 && len(s.Panes) == 0:
		return fmt.Errorf("%w: a scope names environments or panes, or is all", ErrInvalid)
	}
	for _, id := range slices.Concat(s.Environments, s.Panes) {
		if id == "" || strings.ContainsAny(id, " \t\n") {
			return fmt.Errorf("%w: scope entry %q", ErrInvalid, id)
		}
	}
	return nil
}

// Environment reports whether the whole environment id is in scope.
func (s Scope) Environment(id string) bool {
	return s.All || (id != "" && slices.Contains(s.Environments, id))
}

// Pane reports whether pane id, in environment envID, is in scope.
func (s Scope) Pane(envID, id string) bool {
	return s.Environment(envID) || (id != "" && slices.Contains(s.Panes, id))
}

// Sees reports whether something at t is in scope. Something that belongs
// to no environment is seen only with All.
func (s Scope) Sees(t Target) bool {
	switch {
	case t.Environment == "" && t.Pane == "":
		return s.All
	case t.Pane != "":
		return s.Pane(t.Environment, t.Pane)
	default:
		return s.Environment(t.Environment)
	}
}

func (s Scope) String() string {
	if s.All {
		return "all"
	}
	var parts []string
	for _, e := range s.Environments {
		parts = append(parts, "environment:"+e)
	}
	for _, p := range s.Panes {
		parts = append(parts, "pane:"+p)
	}
	return strings.Join(parts, ",")
}

// Target is where something observable is: an environment, or a pane in
// one. Both empty means the session as a whole.
type Target struct {
	Environment string `json:"environment,omitempty"`
	Pane        string `json:"pane,omitempty"`
}

// Credential states.
const (
	StateActive  = "active"
	StateExpired = "expired"
	StateRevoked = "revoked"
)

// Credential is one observer's access. Its token is shown once, when it is
// created; the daemon keeps only a hash of it.
type Credential struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Scope Scope  `json:"scope"`
	// Screens allows screen contents and terminal output; without it the
	// observer sees only metadata and events.
	Screens    bool       `json:"screens"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	// State is filled on reads: active, expired or revoked.
	State string `json:"state,omitempty"`

	hash string // hex SHA-256 of the token's secret
}

// StateAt says whether c is active, expired or revoked at now.
func (c Credential) StateAt(now time.Time) string {
	switch {
	case c.RevokedAt != nil:
		return StateRevoked
	case !now.Before(c.ExpiresAt):
		return StateExpired
	default:
		return StateActive
	}
}

// Permissions an observer method needs besides being in scope.
const (
	// PermissionScreens reads screen contents and terminal output.
	PermissionScreens = "screens"
)
