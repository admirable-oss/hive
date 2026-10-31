package mux

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/admirable-oss/hive/internal/access"
	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/presence"
)

// Working with others (RFC 0007): who this client is, who else is here and
// where, who is typing where, and, for a member, a focus of their own.

// selfState is access.whoami: the runtime's own user (member nil), or a
// member with their grants. A daemon without members says nothing: the
// runtime's own user.
type selfState struct {
	Owner  bool           `json:"owner"`
	Member *access.Member `json:"member"`
}

// presenceState is presence.list.
type presenceState struct {
	Presence []presence.Entry `json:"presence"`
	Holds    []presence.Hold  `json:"holds"`
}

func loadSelf(ctx context.Context, c client.Client) selfState {
	var s selfState
	_ = c.Call(ctx, "access.whoami", nil, &s)
	return s
}

func loadPresence(ctx context.Context, c client.Client) presenceState {
	var p presenceState
	_ = c.Call(ctx, "presence.list", nil, &p)
	return p
}

// ownFocus is a member's own focus, which the session's (the owner's) does
// not move: the pane focused in each tab, the tab shown in each
// environment.
type ownFocus struct {
	panes map[string]string // tab → pane
	tabs  map[string]string // environment → tab
}

func (a *App) member() *access.Member {
	if a.ws.snap == nil {
		return nil
	}
	return a.ws.snap.self.Member
}

// selfID is who this client types as.
func (a *App) selfID() string {
	if m := a.member(); m != nil {
		return m.ID
	}
	return presence.OwnerID
}

// mayIn reports whether this client may act with role in environment env:
// always for the runtime's own user.
func (a *App) mayIn(env string, role access.Role) bool {
	m := a.member()
	return m == nil || m.RoleIn(env).AtLeast(role)
}

// focusedOwn records a focus a member moved to, and shows it.
func (a *App) focusedOwn(p pane.Pane) {
	if a.member() == nil || p.ID == "" {
		return
	}
	if a.ws.own.panes == nil {
		a.ws.own = ownFocus{panes: map[string]string{}, tabs: map[string]string{}}
	}
	a.ws.own.panes[p.TabID] = p.ID
	a.ws.own.tabs[p.EnvironmentID] = p.TabID
	a.applyOwnFocus()
	a.afterChange()
}

// shownOwnTab records the tab a member switched to.
func (a *App) shownOwnTab(t *pane.Tab) {
	if a.member() == nil || t == nil {
		return
	}
	if a.ws.own.tabs == nil {
		a.ws.own = ownFocus{panes: map[string]string{}, tabs: map[string]string{}}
	}
	a.ws.own.tabs[t.EnvironmentID] = t.ID
}

// applyOwnFocus puts a member's own focus over the session's in the
// snapshot, after every read and layout change.
func (a *App) applyOwnFocus() {
	s := a.ws.snap
	if s == nil || a.member() == nil {
		return
	}
	for tabID, paneID := range a.ws.own.panes {
		t := s.tab(tabID)
		if t == nil || !slices.ContainsFunc(s.panes, func(p pane.Pane) bool { return p.ID == paneID && p.TabID == tabID }) {
			delete(a.ws.own.panes, tabID) // the pane closed or moved
			continue
		}
		t.Focused = paneID
	}
	for envID, tabID := range a.ws.own.tabs {
		if s.tab(tabID) == nil {
			delete(a.ws.own.tabs, envID)
			continue
		}
		for _, t := range s.tabsOf(envID) {
			t.Active = t.ID == tabID
		}
	}
}

// reportPresence tells the daemon where this client is looking, when it
// moved. A daemon without presence is not asked again.
func (a *App) reportPresence() {
	s := a.ws.snap
	if s == nil || a.ws.noPresence {
		return
	}
	at := presence.Spot{Environment: a.ws.envID}
	if t := a.tab(); t != nil {
		at.Tab, at.Pane = t.ID, t.Focused
	}
	if at == a.ws.reported {
		return
	}
	a.ws.reported = at
	c := a.c
	a.spawn(func(ctx context.Context) Msg {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		err := c.Call(ctx, "presence.set", at, nil)
		return presenceReportedMsg{unsupported: isCode(err, "unknown_method")}
	})
}

// presenceReportedMsg is a finished presence.set.
type presenceReportedMsg struct{ unsupported bool }

// applyPresenceEvent patches presence and holders from an event; it
// reports whether ev was one.
func (a *App) applyPresenceEvent(ev event.Event) bool {
	s := a.ws.snap
	switch ev.Type {
	case "presence.changed":
		var p presenceState
		if json.Unmarshal(ev.Data, &p) == nil {
			s.presence.Presence, a.dirty = p.Presence, true
		}
		return true
	case "pane.floor":
		var h struct {
			presence.Hold
			Holder *presence.Person `json:"holder"`
		}
		if json.Unmarshal(ev.Data, &h) != nil {
			return true
		}
		s.presence.Holds = slices.DeleteFunc(s.presence.Holds, func(x presence.Hold) bool { return x.Process == h.Process })
		if h.Holder != nil {
			h.Hold.Holder = *h.Holder
			s.presence.Holds = append(s.presence.Holds, h.Hold)
		}
		a.dirty = true
		return true
	}
	return false
}

// typingIn names someone else holding pane p's input ("" for nobody, or
// this client's own person).
func (a *App) typingIn(p *pane.Pane) string {
	s := a.ws.snap
	if s == nil {
		return ""
	}
	for _, h := range s.presence.Holds {
		if h.Process == p.ProcessID && h.Holder.ID != a.selfID() {
			return h.Holder.Name
		}
	}
	return ""
}

// othersAt names the other people looking at pane p, or (pane "") in
// environment env.
func (a *App) othersAt(env, paneID string) []string {
	s := a.ws.snap
	if s == nil {
		return nil
	}
	var names []string
	for _, e := range s.presence.Presence {
		if e.Person.ID == a.selfID() || (paneID != "" && e.Pane != paneID) || (paneID == "" && e.Environment != env) {
			continue
		}
		if !slices.Contains(names, e.Person.Name) {
			names = append(names, e.Person.Name)
		}
	}
	slices.Sort(names)
	return names
}

// withOthers is the tab bar's note of who else is in the environment on
// screen.
func (a *App) withOthers() string {
	names := a.othersAt(a.ws.envID, "")
	if len(names) == 0 {
		return ""
	}
	if len(names) > 3 {
		names = append(names[:3], "…")
	}
	return "with " + strings.Join(names, ", ")
}
