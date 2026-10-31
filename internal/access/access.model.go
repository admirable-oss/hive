// Package access lets people other than the runtime's user work in it:
// members (RFC 0007).
//
// A member is a person the user let in, with a role per environment and an
// expiry. A member reaches the daemon through ssh, with a key whose line in
// authorized_keys forces `hive proxy --member <id>`; the proxy binds the
// connection to that member on the daemon's member socket before it relays
// anything, so nothing the client sends can say who it is. The daemon
// re-checks the member before every request, and revoking one ends its
// connections at once.
//
// As with observers, this is access control in the API, not isolation in
// the operating system: a process running as the daemon's user can open
// any of its sockets. The boundary holds when the member's key, restricted
// as Hive writes it, is their only way into the account.
package access

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
)

// Role is what a member may do in an environment. Roles are ordered:
// each allows what the one before it does, and more.
type Role string

// Roles, lowest first. RoleNone in a grant takes a role away.
const (
	RoleNone     Role = "none"
	RoleObserver Role = "observer"
	RoleOperator Role = "operator"
	RoleOwner    Role = "owner"
)

// Roles lists the roles a grant may give, lowest first.
var Roles = []Role{RoleObserver, RoleOperator, RoleOwner}

func (r Role) rank() int {
	switch r {
	case RoleObserver:
		return 1
	case RoleOperator:
		return 2
	case RoleOwner:
		return 3
	}
	return 0
}

// AtLeast reports whether r allows what least does.
func (r Role) AtLeast(least Role) bool { return r.rank() >= least.rank() && r.rank() > 0 }

// ParseRole reads a role's name; "none" is accepted for taking one away.
func ParseRole(s string) (Role, error) {
	r := Role(strings.ToLower(strings.TrimSpace(s)))
	if r == RoleNone || r.rank() > 0 {
		return r, nil
	}
	return "", fmt.Errorf("%w: role %q (observer, operator or owner)", ErrInvalid, s)
}

// AllEnvironments is a grant's Environment for every environment of the
// session, those made later included.
const AllEnvironments = "*"

// Grant gives a member a role in one environment, or in all of them.
type Grant struct {
	Environment string `json:"environment"`
	Role        Role   `json:"role"`
}

func (g Grant) String() string {
	env := g.Environment
	if env == AllEnvironments {
		env = "all"
	}
	return string(g.Role) + "@" + env
}

// Member states.
const (
	StateActive  = "active"
	StateExpired = "expired"
	StateRevoked = "revoked"
)

// Member is one person's access to this runtime.
type Member struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Key is the member's ssh public key, as "<type> <base64>".
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
	// Managed means Hive keeps the key's line in authorized_keys; false
	// when the line was printed for someone else to install.
	Managed    bool       `json:"managed"`
	Grants     []Grant    `json:"grants"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	// State is filled on reads: active, expired or revoked.
	State string `json:"state,omitempty"`
}

// StateAt says whether m is active, expired or revoked at now.
func (m Member) StateAt(now time.Time) string {
	switch {
	case m.RevokedAt != nil:
		return StateRevoked
	case !now.Before(m.ExpiresAt):
		return StateExpired
	default:
		return StateActive
	}
}

// RoleIn is the member's role in environment env: the higher of its grant
// there and its grant for all environments. RoleNone means none.
func (m Member) RoleIn(env string) Role {
	best := RoleNone
	for _, g := range m.Grants {
		if (g.Environment == AllEnvironments || (env != "" && g.Environment == env)) && g.Role.rank() > best.rank() {
			best = g.Role
		}
	}
	return best
}

// GrantsString lists the grants for people: "operator@web, observer@all".
func (m Member) GrantsString() string {
	if len(m.Grants) == 0 {
		return "none"
	}
	parts := make([]string, len(m.Grants))
	for i, g := range m.Grants {
		parts[i] = g.String()
	}
	return strings.Join(parts, ", ")
}

// Errors the store returns.
var (
	ErrInvalid  = errors.New("access: invalid")
	ErrNotFound = errors.New("access: no such member")
	ErrExpired  = errors.New("access: member expired")
	ErrRevoked  = errors.New("access: member revoked")
	ErrConflict = errors.New("access: conflict")
)

// validName accepts 1 to 32 letters, digits, '.', '_' and '-', starting
// with a letter or digit: names appear in authorized_keys and commands.
func validName(s string) bool {
	if s == "" || len(s) > 32 || !unicode.IsLetter(rune(s[0])) && !unicode.IsDigit(rune(s[0])) {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool {
		return r > unicode.MaxASCII || !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("._-", r)
	})
}

// idPrefix starts every member ID.
const idPrefix = "m_"

// ValidID reports whether s looks like a member ID: what `hive proxy
// --member` accepts before asking the daemon.
func ValidID(s string) bool {
	rest, ok := strings.CutPrefix(s, idPrefix)
	return ok && len(rest) == 10 && !strings.ContainsFunc(rest, func(r rune) bool {
		return (r < '0' || r > '9') && (r < 'a' || r > 'f')
	})
}

func validGrants(gs []Grant) error {
	seen := map[string]bool{}
	for _, g := range gs {
		if g.Environment == "" || strings.ContainsAny(g.Environment, " \t\n") {
			return fmt.Errorf("%w: grant environment %q", ErrInvalid, g.Environment)
		}
		if g.Role.rank() == 0 {
			return fmt.Errorf("%w: grant role %q (observer, operator or owner)", ErrInvalid, g.Role)
		}
		if seen[g.Environment] {
			return fmt.Errorf("%w: two grants for environment %q", ErrInvalid, g.Environment)
		}
		seen[g.Environment] = true
	}
	return nil
}

// sortGrants puts the all-environments grant first, then by environment.
func sortGrants(gs []Grant) {
	slices.SortFunc(gs, func(a, b Grant) int {
		switch {
		case a.Environment == b.Environment:
			return 0
		case a.Environment == AllEnvironments:
			return -1
		case b.Environment == AllEnvironments:
			return 1
		}
		return strings.Compare(a.Environment, b.Environment)
	})
}

// Caller is who a request on the member socket comes from.
type Caller struct {
	Member Member `json:"member"`
	// From is where the ssh connection came from ("ip port"), as ssh told
	// the proxy; empty for a local connection.
	From string `json:"from,omitempty"`
}

type callerKey struct{}

// WithCaller records the member a request belongs to.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerOf returns the member a request belongs to; ok is false for the
// runtime's own user (the control socket, plugins acting for them).
func CallerOf(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}
