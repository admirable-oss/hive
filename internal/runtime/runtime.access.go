package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/access"
	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/protocol"
)

// MemberSocketName is the member socket's name in the storage root.
const MemberSocketName = "member.sock"

// MemberSocketPath is where the daemon storing its data in root listens
// for members (`hive proxy --member`).
func MemberSocketPath(root string) string {
	return platform.SocketPath(root, MemberSocketName)
}

// AccessConfig is the [access] policy.
type AccessConfig struct {
	// Disabled turns the member socket off; members are kept.
	Disabled bool
	// MaxConnections bounds open member connections; zero means
	// access.DefaultMaxConnections.
	MaxConnections int
	// AuthorizedKeys is the file members' keys are kept in; empty means
	// ~/.ssh/authorized_keys.
	AuthorizedKeys string
	// ProxyCommand is the shell command a member's key is forced to run,
	// up to the member's ID: `<hive> [--session s] proxy --member`.
	// Empty means "hive proxy --member".
	ProxyCommand string
}

// EventAccessChanged says a member was invited, granted, renewed or
// revoked, or connected or left.
const EventAccessChanged = "access.changed"

// accessEvent is EventAccessChanged's data.
type accessEvent struct {
	Member string `json:"member,omitempty"`
	Name   string `json:"name,omitempty"`
	Action string `json:"action"`
}

// keysSweep is how often the authorized_keys block is brought up to date
// with members that expired since.
const keysSweep = time.Hour

// members serves the member socket (see package access): people let in
// through ssh, each bound to a member by their key's forced command.
type members struct {
	cfg    Config
	log    *slog.Logger
	dir    string
	path   string
	store  *access.Store
	server *access.Server
	bus    *event.Bus

	keysMu sync.Mutex

	auditOnce sync.Once
	audit     *access.Audit
}

func newMembers(cfg Config, log *slog.Logger, bus *event.Bus, router *protocol.Router, guard *memberGuard) *members {
	dir := filepath.Join(cfg.root(), "access")
	m := &members{cfg: cfg, log: log, dir: dir, path: MemberSocketPath(cfg.root()), bus: bus, store: access.NewStore(dir)}
	guard.router, guard.audit = router, m.auditLog
	m.server = access.NewServer(access.Config{
		Store: m.store, Handler: guard, Methods: router.Methods, Allowed: memberAllowed, Logger: log,
		MaxConnections: cfg.Access.MaxConnections,
		OnChange:       func() { bus.Publish(EventAccessChanged, accessEvent{Action: "connections"}) },
	})
	return m
}

// auditLog opens the audit log on first use.
func (m *members) auditLog() *access.Audit {
	m.auditOnce.Do(func() {
		a, err := access.OpenAudit(m.dir)
		if err != nil {
			m.log.Warn("access audit log unavailable", "err", err)
			return
		}
		m.audit = a
	})
	return m.audit
}

func (m *members) keysFile() access.KeysFile {
	path := cmp(m.cfg.Access.AuthorizedKeys, "~/.ssh/authorized_keys")
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return access.KeysFile{}
		}
		path = filepath.Join(home, rest)
	}
	return access.KeysFile{Path: path}
}

func (m *members) proxyCommand() string {
	if c := strings.TrimSpace(m.cfg.Access.ProxyCommand); c != "" {
		return c
	}
	return "hive proxy --member"
}

// line is member mem's authorized_keys line.
func (m *members) line(mem access.Member) string { return access.AuthorizedLine(m.proxyCommand(), mem) }

// syncKeys rewrites Hive's block in authorized_keys to hold exactly the
// active members whose keys Hive manages.
func (m *members) syncKeys() error {
	m.keysMu.Lock()
	defer m.keysMu.Unlock()
	f := m.keysFile()
	if f.Path == "" {
		return errors.New("no home directory for authorized_keys")
	}
	all, err := m.store.List()
	if err != nil {
		return err
	}
	var lines []string
	for _, mem := range all {
		if mem.Managed && mem.State == access.StateActive {
			lines = append(lines, m.line(mem))
		}
	}
	current, _, err := f.Read()
	if err == nil && slices.Equal(current, lines) {
		return nil
	}
	if err := f.Write(lines); err != nil {
		m.auditLog().Record(access.AuditEntry{Action: access.AuditKeys, Detail: "not updated: " + err.Error()})
		return err
	}
	m.auditLog().Record(access.AuditEntry{Action: access.AuditKeys, Detail: fmt.Sprintf("%s holds %d member keys", f.Path, len(lines))})
	return nil
}

// run serves the member socket until ctx ends.
func (m *members) run(ctx context.Context) {
	m.server.SetAudit(m.auditLog())
	if err := m.syncKeys(); err != nil {
		m.log.Warn("authorized_keys not brought up to date", "err", err)
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Go(func() {
		t := time.NewTicker(keysSweep)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = m.syncKeys()
			}
		}
	})
	if m.cfg.Access.Disabled {
		return
	}
	_ = os.Remove(m.path) // left by a daemon that crashed; the daemon lock is ours
	ln, err := m.cfg.listener().Listen("unix", m.path)
	if err != nil {
		m.log.Warn("member socket unavailable", "path", m.path, "err", err)
		return
	}
	if err := os.Chmod(m.path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.log.Warn("member socket not secured; closing it", "err", err)
		_ = ln.Close()
		return
	}
	stop := context.AfterFunc(ctx, func() {
		_ = ln.Close()
		m.server.Close() // members do not hold the daemon up
	})
	defer stop()
	defer os.Remove(m.path)
	info := protocol.ServerInfo{Version: buildinfo.Get().Version}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		if err := platform.CheckPeer(conn); err != nil {
			m.log.Warn("refused a connection to the member socket", "err", err)
			_ = conn.Close()
			continue
		}
		wg.Go(func() { m.server.Serve(ctx, conn, info) })
	}
}

func (m *members) changed(mem access.Member, action string) {
	m.bus.Publish(EventAccessChanged, accessEvent{Member: mem.ID, Name: mem.Name, Action: action})
}

// Access methods. Managing members is on the control socket only, that is
// the runtime's own user: the member socket allows members none of them,
// and no plugin may call them. access.whoami answers everyone.

// AccessInviteParams describes a new member.
type AccessInviteParams struct {
	Name string `json:"name"`
	// Key is the member's ssh public key, as in a .pub file.
	Key    string         `json:"key"`
	Grants []access.Grant `json:"grants"`
	// TTLSeconds is how long it lasts; zero means access.DefaultTTL.
	TTLSeconds int64 `json:"ttl_seconds,omitempty"`
	// Print leaves authorized_keys alone: the line is returned for someone
	// else to install.
	Print bool `json:"print,omitempty"`
}

// AccessInvited is a new member and the authorized_keys line that lets it
// in.
type AccessInvited struct {
	Member access.Member `json:"member"`
	Line   string        `json:"line"`
	// AuthorizedKeys is the file the line was added to; empty with print.
	AuthorizedKeys string `json:"authorized_keys,omitempty"`
	Socket         string `json:"socket"`
}

// AccessMember is a member and its open connections.
type AccessMember struct {
	access.Member
	Connections int `json:"connections"`
}

// AccessList is access.list.
type AccessList struct {
	Socket         string         `json:"socket"`
	Enabled        bool           `json:"enabled"`
	AuthorizedKeys string         `json:"authorized_keys"`
	Members        []AccessMember `json:"members"`
}

type accessRef struct {
	Member string `json:"member"`
}

type accessGrantParams struct {
	Member string `json:"member"`
	// Environment is an environment's ID, or "*" for all of them.
	Environment string      `json:"environment"`
	Role        access.Role `json:"role" desc:"observer, operator, owner, or none to take the role away"`
}

type accessRenewParams struct {
	Member     string `json:"member"`
	TTLSeconds int64  `json:"ttl_seconds,omitempty"`
}

type accessAuditParams struct {
	Member string `json:"member,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type accessDoctorParams struct {
	// Fix rewrites Hive's block in authorized_keys to match the members.
	Fix bool `json:"fix,omitempty"`
}

// AccessDoctor is what access.doctor found.
type AccessDoctor struct {
	AuthorizedKeys string   `json:"authorized_keys"`
	Problems       []string `json:"problems"`
	Fixed          bool     `json:"fixed,omitempty"`
}

// WhoAmI is who a connection is.
type WhoAmI struct {
	// Owner is the runtime's own user: the control socket.
	Owner  bool           `json:"owner"`
	Member *access.Member `json:"member,omitempty"`
	From   string         `json:"from,omitempty"`
}

func registerAccess(r *protocol.Router, m *members, envs environment.Service) {
	checkEnvs := func(ctx context.Context, grants []access.Grant) error {
		for _, g := range grants {
			if g.Environment == access.AllEnvironments {
				continue
			}
			if _, err := envs.Get(ctx, g.Environment); err != nil {
				return protocol.NewError(protocol.ErrorCodeNotFound, fmt.Errorf("environment %q: %w", g.Environment, err))
			}
		}
		return nil
	}
	r.MustRegister("access.invite", protocol.Method(func(ctx context.Context, p AccessInviteParams) (AccessInvited, error) {
		key, err := access.ParseKey(p.Key)
		if err != nil {
			return AccessInvited{}, accessError(err)
		}
		if err := checkEnvs(ctx, p.Grants); err != nil {
			return AccessInvited{}, err
		}
		f := m.keysFile()
		if !p.Print {
			// A key that already logs in without restriction would never
			// be held to a role: ssh uses the first line that matches.
			_, others, err := f.Read()
			if err != nil {
				return AccessInvited{}, err
			}
			for _, k := range others {
				if k.String() == key.String() {
					return AccessInvited{}, protocol.NewError(protocol.ErrorCodeConflict, fmt.Errorf(
						"this key already logs in through %s without Hive's restriction; remove that line first, or the member would not be held to a role", f.Path))
				}
			}
		}
		mem, err := m.store.Invite(access.InviteRequest{
			Name: p.Name, Key: key, Grants: p.Grants, TTL: time.Duration(p.TTLSeconds) * time.Second, Managed: !p.Print,
		})
		if err != nil {
			return AccessInvited{}, accessError(err)
		}
		out := AccessInvited{Member: mem, Line: m.line(mem), Socket: m.path}
		if !p.Print {
			if err := m.syncKeys(); err != nil {
				_, _ = m.store.Revoke(mem.ID) // not let in after all
				return AccessInvited{}, fmt.Errorf("add the key to %s: %w", f.Path, err)
			}
			out.AuthorizedKeys = f.Path
		}
		m.auditLog().Record(access.AuditEntry{
			Action: access.AuditInvited, Member: mem.ID, Name: mem.Name,
			Detail: fmt.Sprintf("%s, key %s, expires %s", mem.GrantsString(), mem.Fingerprint, mem.ExpiresAt.Format(time.RFC3339)),
		})
		m.changed(mem, access.AuditInvited)
		return out, nil
	}))
	r.MustRegister("access.list", protocol.Method(func(context.Context, protocol.Empty) (AccessList, error) {
		all, err := m.store.List()
		if err != nil {
			return AccessList{}, err
		}
		conns := m.server.Connections()
		out := AccessList{Socket: m.path, Enabled: !m.cfg.Access.Disabled, AuthorizedKeys: m.keysFile().Path, Members: []AccessMember{}}
		for _, mem := range all {
			out.Members = append(out.Members, AccessMember{Member: mem, Connections: conns[mem.ID]})
		}
		return out, nil
	}))
	r.MustRegister("access.get", protocol.Method(func(_ context.Context, p accessRef) (AccessMember, error) {
		mem, err := m.store.Get(p.Member)
		if err != nil {
			return AccessMember{}, accessError(err)
		}
		return AccessMember{Member: mem, Connections: m.server.Connections()[mem.ID]}, nil
	}))
	r.MustRegister("access.grant", protocol.Method(func(ctx context.Context, p accessGrantParams) (access.Member, error) {
		role, err := access.ParseRole(string(p.Role))
		if err != nil {
			return access.Member{}, accessError(err)
		}
		if role != access.RoleNone {
			if err := checkEnvs(ctx, []access.Grant{{Environment: p.Environment, Role: role}}); err != nil {
				return access.Member{}, err
			}
		}
		mem, err := m.store.Grant(p.Member, p.Environment, role)
		if err != nil {
			return access.Member{}, accessError(err)
		}
		m.server.Changed(mem.ID)
		m.auditLog().Record(access.AuditEntry{Action: access.AuditGranted, Member: mem.ID, Name: mem.Name, Detail: mem.GrantsString()})
		m.changed(mem, access.AuditGranted)
		return mem, nil
	}))
	r.MustRegister("access.renew", protocol.Method(func(_ context.Context, p accessRenewParams) (access.Member, error) {
		mem, err := m.store.Renew(p.Member, time.Duration(p.TTLSeconds)*time.Second)
		if err != nil {
			return access.Member{}, accessError(err)
		}
		m.server.Changed(mem.ID)
		m.auditLog().Record(access.AuditEntry{Action: access.AuditRenewed, Member: mem.ID, Name: mem.Name, Detail: "expires " + mem.ExpiresAt.Format(time.RFC3339)})
		m.changed(mem, access.AuditRenewed)
		return mem, nil
	}))
	r.MustRegister("access.revoke", protocol.Method(func(_ context.Context, p accessRef) (access.Member, error) {
		mem, err := m.store.Revoke(p.Member)
		if err != nil {
			return access.Member{}, accessError(err)
		}
		m.server.Revoked(mem.ID)
		m.auditLog().Record(access.AuditEntry{Action: access.AuditRevoked, Member: mem.ID, Name: mem.Name})
		if err := m.syncKeys(); err != nil {
			// Revoked all the same: the member socket refuses it.
			m.log.Warn("revoked member's key left in authorized_keys", "member", mem.Name, "err", err)
		}
		m.changed(mem, access.AuditRevoked)
		return mem, nil
	}))
	r.MustRegister("access.audit", protocol.Method(func(_ context.Context, p accessAuditParams) ([]access.AuditEntry, error) {
		entries, err := m.auditLog().Read(p.Limit, p.Member)
		if entries == nil {
			entries = []access.AuditEntry{}
		}
		return entries, err
	}))
	r.MustRegister("access.doctor", protocol.Method(func(_ context.Context, p accessDoctorParams) (AccessDoctor, error) {
		return m.doctor(p.Fix)
	}))
	r.MustRegister("access.whoami", protocol.Method(func(ctx context.Context, _ protocol.Empty) (WhoAmI, error) {
		c, ok := access.CallerOf(ctx)
		if !ok {
			return WhoAmI{Owner: true}, nil
		}
		return WhoAmI{Member: &c.Member, From: c.From}, nil
	}))
}

// doctor compares authorized_keys with the members; with fix it rewrites
// Hive's block to match them.
func (m *members) doctor(fix bool) (AccessDoctor, error) {
	f := m.keysFile()
	out := AccessDoctor{AuthorizedKeys: f.Path, Problems: []string{}}
	all, err := m.store.List()
	if err != nil {
		return out, err
	}
	managed, others, err := f.Read()
	if err != nil {
		return out, err
	}
	want := map[string]access.Member{}
	for _, mem := range all {
		if mem.Managed && mem.State == access.StateActive {
			want[m.line(mem)] = mem
		}
	}
	for line, mem := range want {
		if !slices.Contains(managed, line) {
			out.Problems = append(out.Problems, fmt.Sprintf("member %s has no line in %s, or it was changed: it cannot connect", mem.Name, f.Path))
		}
	}
	for _, line := range managed {
		if _, ok := want[line]; !ok {
			out.Problems = append(out.Problems, fmt.Sprintf("a line in Hive's block belongs to no active member: %.60s…", line))
		}
	}
	for _, mem := range all {
		if mem.State != access.StateActive {
			continue
		}
		for _, k := range others {
			if k.String() == mem.Key {
				out.Problems = append(out.Problems, fmt.Sprintf("member %s's key also logs in outside Hive's block, without restriction: remove that line", mem.Name))
			}
		}
	}
	slices.Sort(out.Problems)
	if fix {
		if err := m.syncKeys(); err != nil {
			return out, err
		}
		out.Fixed = true
	}
	return out, nil
}

func accessError(err error) error {
	switch {
	case errors.Is(err, access.ErrNotFound):
		return protocol.NewError(protocol.ErrorCodeNotFound, err)
	case errors.Is(err, access.ErrInvalid):
		return protocol.NewError(protocol.ErrorCodeInvalidParams, err)
	case errors.Is(err, access.ErrConflict), errors.Is(err, access.ErrExpired), errors.Is(err, access.ErrRevoked):
		return protocol.NewError(protocol.ErrorCodeConflict, err)
	}
	return err
}
