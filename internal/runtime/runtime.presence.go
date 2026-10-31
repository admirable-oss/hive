package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/user"

	"github.com/admirable-oss/hive/internal/access"
	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/presence"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/terminal"
)

// Shared workspaces (RFC 0007): who is where, and one typist per terminal.

// Presence events.
const (
	// EventPresenceChanged carries everyone's presence after a change:
	// PresenceList's presence.
	EventPresenceChanged = "presence.changed"
	// EventPaneFloor says who holds a terminal's input now: a
	// presence.Hold, or its process with no holder once it is free.
	EventPaneFloor = "pane.floor"
)

// ownerPerson is the runtime's own user, by their login name.
func ownerPerson() presence.Person {
	name := "owner"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	return presence.Person{ID: presence.OwnerID, Name: name}
}

// personOf is who a request comes from: its member, or the runtime's own
// user.
func personOf(ctx context.Context, owner presence.Person) presence.Person {
	if c, ok := access.CallerOf(ctx); ok {
		return presence.Person{ID: c.Member.ID, Name: c.Member.Name}
	}
	return owner
}

// newPresence returns the registry, publishing its changes on bus.
func newPresence(cfg Config, bus *event.Bus, panes *pane.Service) *presence.Registry {
	var reg *presence.Registry
	reg = presence.New(cfg.Access.InputIdle, func(kind string, h *presence.Hold, process string) {
		switch kind {
		case presence.ChangedPresence:
			bus.Publish(EventPresenceChanged, map[string]any{"presence": reg.List()})
		case presence.ChangedFloor:
			if h != nil {
				bus.Publish(EventPaneFloor, h)
				return
			}
			free := map[string]any{"process_id": process, "holder": nil}
			if pn, ok := paneOfProcess(context.Background(), panes, process); ok {
				free["pane"], free["environment"] = pn.ID, pn.EnvironmentID
			}
			bus.Publish(EventPaneFloor, free)
		}
	})
	return reg
}

func paneOfProcess(ctx context.Context, panes *pane.Service, process string) (pane.Pane, bool) {
	list, err := panes.Panes(ctx, "", "")
	if err != nil {
		return pane.Pane{}, false
	}
	for _, pn := range list {
		if pn.ProcessID == process {
			return pn, true
		}
	}
	return pane.Pane{}, false
}

// inputTargets are the methods that type into a terminal, and the
// parameter naming it.
var inputTargets = map[string]ruleParam{
	"pane.input": p("id", targetPane), "pane.send_keys": p("id", targetPane),
	"pane.send_text": p("id", targetPane), "pane.run": p("id", targetPane),
	"agent.prompt": p("id", targetAgent), "agent.send_keys": p("id", targetAgent),
	"terminal.input": p("process_id", targetProcess), "terminal.image": p("process_id", targetProcess),
	// Pipes: each piece typed is checked as it comes.
	"terminal.attach": p("process_id", targetProcess), "terminal.frames": p("process_id", targetProcess),
}

// inputGuard is in front of the daemon's API for everyone, the runtime's
// own user included: input into a terminal someone else holds is refused,
// so people never type into one prompt at once.
type inputGuard struct {
	next   *protocol.Router
	reg    *presence.Registry
	panes  *pane.Service
	agents *agent.Service
	owner  presence.Person
}

func (h *inputGuard) Methods() []string { return h.next.Methods() }

func (h *inputGuard) Handle(ctx context.Context, req protocol.Request) protocol.Response {
	target, ok := inputTargets[req.Method]
	if !ok {
		return h.next.Handle(ctx, req)
	}
	at, err := h.locate(ctx, target, req.Params)
	if err != nil {
		return h.next.Handle(ctx, req) // the method answers for what does not exist
	}
	who := personOf(ctx, h.owner)
	if req.Method == "terminal.attach" || req.Method == "terminal.frames" {
		ctx = terminal.WithInputGate(ctx, func() bool { return h.reg.Use(at.process, at.pane, at.env, who) == nil })
		return h.next.Handle(ctx, req)
	}
	if err := h.reg.Use(at.process, at.pane, at.env, who); err != nil {
		return protocol.Fail(req, protocol.NewError(protocol.ErrorCodeConflict, err))
	}
	return h.next.Handle(ctx, req)
}

// terminalAt is a terminal: its process, and the pane and environment
// showing it.
type terminalAt struct{ process, pane, env string }

func (h *inputGuard) locate(ctx context.Context, target ruleParam, raw json.RawMessage) (terminalAt, error) {
	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return terminalAt{}, err
	}
	var id string
	if err := json.Unmarshal(params[target.name], &id); err != nil || id == "" {
		return terminalAt{}, errors.New("no target")
	}
	switch target.kind {
	case targetPane:
		pn, err := h.panes.Pane(ctx, id)
		if err != nil || pn.ProcessID == "" {
			return terminalAt{}, errors.New("no terminal")
		}
		return terminalAt{pn.ProcessID, pn.ID, pn.EnvironmentID}, nil
	case targetAgent:
		a, err := h.agents.Get(ctx, id)
		if err != nil {
			return terminalAt{}, err
		}
		return terminalAt{a.ID, a.PaneID, a.EnvironmentID}, nil
	case targetProcess:
		at := terminalAt{process: id}
		if pn, ok := paneOfProcess(ctx, h.panes, id); ok {
			at.pane, at.env = pn.ID, pn.EnvironmentID
		}
		return at, nil
	}
	return terminalAt{}, fmt.Errorf("unknown target kind %d", target.kind)
}

// PresenceList is presence.list: who is connected and where, and who
// holds which terminal.
type PresenceList struct {
	Presence []presence.Entry `json:"presence"`
	Holds    []presence.Hold  `json:"holds"`
}

type presenceSetParams struct {
	Environment string `json:"environment,omitempty"`
	Tab         string `json:"tab,omitempty"`
	Pane        string `json:"pane,omitempty"`
}

type paneClaimParams struct {
	Pane string `json:"pane"`
	// Force takes it from whoever holds it: the runtime's own user, or an
	// owner of the pane's environment.
	Force bool `json:"force,omitempty"`
}

// PaneClaimed is who holds a pane's input after pane.claim, and who did
// before, if someone else.
type PaneClaimed struct {
	Holder   presence.Person  `json:"holder"`
	Previous *presence.Person `json:"previous,omitempty"`
}

type paneReleaseParams struct {
	Pane string `json:"pane"`
}

func registerPresence(r *protocol.Router, h *inputGuard) {
	r.MustRegister("presence.set", protocol.Method(func(ctx context.Context, p presenceSetParams) (protocol.Empty, error) {
		return protocol.Empty{}, h.setPresence(ctx, presence.Spot{Environment: p.Environment, Tab: p.Tab, Pane: p.Pane})
	}))
	r.MustRegister("presence.list", protocol.Method(func(context.Context, protocol.Empty) (PresenceList, error) {
		return PresenceList{Presence: h.reg.List(), Holds: h.reg.Holds()}, nil
	}))
	r.MustRegister("pane.claim", protocol.Method(func(ctx context.Context, p paneClaimParams) (PaneClaimed, error) {
		pn, err := h.panes.Pane(ctx, p.Pane)
		if err != nil {
			return PaneClaimed{}, protocol.NewError(protocol.ErrorCodeNotFound, err)
		}
		if pn.ProcessID == "" {
			return PaneClaimed{}, protocol.NewError(protocol.ErrorCodeUnavailable, fmt.Errorf("pane %s runs nothing to type into", pn.ID))
		}
		who := personOf(ctx, h.owner)
		if c, ok := access.CallerOf(ctx); ok && p.Force && c.Member.RoleIn(pn.EnvironmentID) != access.RoleOwner {
			return PaneClaimed{}, protocol.NewError(protocol.ErrorCodePermissionDenied, errors.New("taking a pane from its holder needs the owner role in its environment"))
		}
		prev, err := h.reg.Claim(pn.ProcessID, pn.ID, pn.EnvironmentID, who, p.Force)
		if err != nil {
			return PaneClaimed{}, protocol.NewError(protocol.ErrorCodeConflict, err)
		}
		return PaneClaimed{Holder: who, Previous: prev}, nil
	}))
	r.MustRegister("pane.release", protocol.Method(func(ctx context.Context, p paneReleaseParams) (map[string]bool, error) {
		pn, err := h.panes.Pane(ctx, p.Pane)
		if err != nil {
			return nil, protocol.NewError(protocol.ErrorCodeNotFound, err)
		}
		return map[string]bool{"released": h.reg.Release(pn.ProcessID, personOf(ctx, h.owner))}, nil
	}))
}

// setPresence records where the request's connection is looking. A
// connection that has said where it looks leaves presence when it ends.
func (h *inputGuard) setPresence(ctx context.Context, at presence.Spot) error {
	peer := protocol.PeerOf(ctx)
	if peer == nil {
		return protocol.NewError(protocol.ErrorCodeUnsupported, errors.New("presence needs a protocol-2 connection"))
	}
	if at.Pane != "" && (at.Tab == "" || at.Environment == "") {
		if pn, err := h.panes.Pane(ctx, at.Pane); err == nil {
			at.Tab, at.Environment = pn.TabID, pn.EnvironmentID
		}
	}
	if at.Tab != "" && at.Environment == "" {
		if t, err := h.panes.Tab(ctx, at.Tab); err == nil {
			at.Environment = t.EnvironmentID
		}
	}
	if h.reg.Set(peer, personOf(ctx, h.owner), peer.Client, at) {
		context.AfterFunc(ctx, func() { h.reg.Leave(peer) })
	}
	return nil
}

// memberFocus is pane.focus, tab.focus or agent.focus from a member: their
// own focus, kept in presence, never the session's (which every client of
// the runtime's own user follows). It answers what the focus method would.
func (g *memberGuard) memberFocus(ctx context.Context, req protocol.Request) protocol.Response {
	var p struct {
		ID        string           `json:"id"`
		Direction layout.Direction `json:"direction"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil || p.ID == "" {
		return protocol.Fail(req, protocol.NewError(protocol.ErrorCodeInvalidParams, errors.New("give id")))
	}
	var (
		at  presence.Spot
		get string
	)
	switch req.Method {
	case "pane.focus":
		pn, err := g.panes.Pane(ctx, p.ID)
		if err == nil && p.Direction != "" {
			pn, err = g.panes.Neighbor(ctx, p.ID, p.Direction)
		}
		if err != nil {
			return protocol.Fail(req, protocol.NewError(protocol.ErrorCodeNotFound, err))
		}
		at, get = presence.Spot{Environment: pn.EnvironmentID, Tab: pn.TabID, Pane: pn.ID}, "pane.get"
		req.Params, _ = json.Marshal(map[string]string{"id": pn.ID})
	case "tab.focus":
		t, err := g.panes.Tab(ctx, p.ID)
		if err != nil {
			return protocol.Fail(req, protocol.NewError(protocol.ErrorCodeNotFound, err))
		}
		at, get = presence.Spot{Environment: t.EnvironmentID, Tab: t.ID, Pane: t.Focused}, "tab.get"
	case "agent.focus":
		a, err := g.agents.Get(ctx, p.ID)
		if err != nil {
			return protocol.Fail(req, protocol.NewError(protocol.ErrorCodeNotFound, err))
		}
		at, get = presence.Spot{Environment: a.EnvironmentID, Tab: a.TabID, Pane: a.PaneID}, "agent.get"
	}
	if err := g.input.setPresence(ctx, at); err != nil {
		return protocol.Fail(req, err)
	}
	req.Method = get
	return g.router.Handle(ctx, req)
}
