package core

import (
	"context"
	"slices"

	"github.com/admirable-oss/hive/internal/presence"
	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// Working with others (RFC 0007): who else is in the runtime and where they
// look, who is typing into which pane, and where this app looks, so others
// see it too.

// presenceList is presence.list.
type presenceList struct {
	Presence []presence.Entry `json:"presence"`
	Holds    []presence.Hold  `json:"holds"`
}

// readPresence reads who is where; a daemon without presence (older than
// M27) has nobody.
func readPresence(ctx context.Context, c *hiveapi.Client) presenceList {
	var p presenceList
	_ = c.Call(ctx, "presence.list", nil, &p)
	return p
}

// withPresence marks ws with the others: in each environment, on each
// pane, and typing into a pane. self is this app's person; the same person
// elsewhere is not someone else.
func withPresence(ws Workspace, p presenceList, self string) Workspace {
	if self == "" {
		self = presence.OwnerID
	}
	add := func(list []string, name string) []string {
		if slices.Contains(list, name) {
			return list
		}
		return append(list, name)
	}
	ws.Environments, ws.Panes = slices.Clone(ws.Environments), slices.Clone(ws.Panes) // ws's own are not changed
	envs := map[string]int{}
	for i, e := range ws.Environments {
		envs[e.ID] = i
	}
	panes := map[string]int{}
	byProcess := map[string]int{}
	for i, pn := range ws.Panes {
		panes[pn.ID] = i
		if pn.ProcessID != "" {
			byProcess[pn.ProcessID] = i
		}
	}
	for _, e := range p.Presence {
		if e.Person.ID == self {
			continue
		}
		if i, ok := envs[e.Environment]; ok {
			ws.Environments[i].With = add(ws.Environments[i].With, e.Person.Name)
		}
		if i, ok := panes[e.Pane]; ok {
			ws.Panes[i].Watchers = add(ws.Panes[i].Watchers, e.Person.Name)
		}
	}
	for _, h := range p.Holds {
		if h.Holder.ID == self {
			continue
		}
		i, ok := panes[h.Pane]
		if !ok {
			i, ok = byProcess[h.Process]
		}
		if ok {
			ws.Panes[i].Typing = h.Holder.Name
		}
	}
	return ws
}

// lookAt tells the daemon this app looks at a pane (with its tab and
// environment), or at a tab and the pane focused in it. A daemon without
// presence ignores it; looking is never worth an error.
func (h *Hub) lookAt(ctx context.Context, c *hiveapi.Client, tabID, paneID string) {
	h.mu.Lock()
	ws := h.ws
	h.mu.Unlock()
	at := map[string]string{}
	if pn, ok := paneByID(ws, paneID); ok {
		at["pane"], at["tab"], at["environment"] = pn.ID, pn.TabID, pn.EnvironmentID
	} else {
		for _, t := range ws.Tabs {
			if t.ID == tabID {
				at["tab"], at["environment"], at["pane"] = t.ID, t.EnvironmentID, t.Focused
			}
		}
	}
	if len(at) > 0 {
		_ = c.Call(ctx, "presence.set", at, nil)
	}
}
