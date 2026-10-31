package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// Notifications the daemon addresses (RFC 0007): a daemon that decides
// who is told that work needs someone publishes notify events, and the app
// shows only those addressed to its person. Against an older daemon the
// app decides for itself (notificationsLocked, approvalsLocked).

// eventNotify is the daemon's notify event.
const eventNotify = "notify"

// whoAmI is who the app's connection belongs to, when the daemon routes
// notifications: "owner" for the runtime's own user, a member's ID, or ""
// when the daemon does not route them.
func whoAmI(ctx context.Context, c *hiveapi.Client) string {
	var who struct {
		Notify bool `json:"notify"`
		Member *struct {
			ID string `json:"id"`
		} `json:"member"`
	}
	if c.Call(ctx, "access.whoami", nil, &who) != nil || !who.Notify {
		return ""
	}
	if who.Member != nil {
		return who.Member.ID
	}
	return "owner"
}

// routedNote is a notify event.
type routedNote struct {
	Kind  string `json:"kind"`
	Agent *struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Kind          string `json:"kind"`
		State         string `json:"state"`
		Reason        string `json:"reason"`
		EnvironmentID string `json:"environment_id"`
		PaneID        string `json:"pane_id"`
		StateSeq      uint64 `json:"state_seq"`
	} `json:"agent"`
	Task *struct {
		PlanID string `json:"plan_id"`
		ID     string `json:"id"`
	} `json:"task"`
	To []routedPerson `json:"to"`
}

type routedPerson struct {
	ID string `json:"id"`
}

// routedNotification is the system notification for a notify event, when
// it is addressed to this app's person and the settings want it.
func (h *Hub) routedNotification(data json.RawMessage) (Notification, bool) {
	var n routedNote
	if json.Unmarshal(data, &n) != nil {
		return Notification{}, false
	}
	h.mu.Lock()
	self, settings, ws := h.self, h.settings, h.ws
	h.mu.Unlock()
	if self == "" || settings.SystemNotifications || !slices.ContainsFunc(n.To, func(p routedPerson) bool {
		return p.ID == self
	}) {
		return Notification{}, false
	}
	switch {
	case n.Agent != nil:
		a := n.Agent
		if !settings.notifies(a.State) || h.cfg.Focused != nil && h.cfg.Focused() && focusedPanes(ws)[a.PaneID] {
			return Notification{}, false
		}
		verb := map[string]string{"blocked": "needs you", "done": "is done"}[a.State]
		if verb == "" {
			verb = "is " + a.State
		}
		body := a.Reason
		if body == "" {
			body = a.Kind + " in " + a.EnvironmentID
		}
		return Notification{ID: fmt.Sprintf("hive-%s-%d", a.ID, a.StateSeq), Title: cmp(a.Name, a.Kind) + " " + verb, Body: body, AgentID: a.ID, PaneID: a.PaneID}, true
	case n.Task != nil && n.Kind == "approval" && settings.notifies("blocked"):
		return Notification{ID: "plan:" + n.Task.PlanID + "/" + n.Task.ID, Title: "Plan " + n.Task.PlanID, Body: n.Task.ID + " waits for your approval"}, true
	case n.Task != nil && n.Kind == "failed" && settings.notifies("done"):
		return Notification{ID: "plan-failed:" + n.Task.PlanID + "/" + n.Task.ID, Title: "Plan " + n.Task.PlanID, Body: n.Task.ID + " failed"}, true
	}
	return Notification{}, false
}

// focusedPanes are the panes focused in the active tabs.
func focusedPanes(ws Workspace) map[string]bool {
	out := map[string]bool{}
	for _, t := range ws.Tabs {
		if t.Active && t.Focused != "" {
			out[t.Focused] = true
		}
	}
	return out
}
