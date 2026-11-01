package observer

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/admirable-oss/hive/internal/presence"
	"github.com/admirable-oss/hive/internal/protocol"
)

// Shared workspaces (RFC 0007), as observers see them: who is where, and
// who holds a terminal's input. People are named, never identified, and
// only where the credential sees.

const methodPresence = "observe.presence"

// Presence events, as the runtime publishes them.
const (
	// EventPresenceChanged carries Present for everyone, after a change
	// (observers that see all; others read observe.presence).
	EventPresenceChanged = "presence.changed"
	// EventPaneFloor carries Floor: who holds a pane's input now.
	EventPaneFloor = "pane.floor"
)

// Present is a person somewhere the credential sees.
type Present struct {
	Name string `json:"name"`
	// Owner is the runtime's own user, as against a member.
	Owner       bool      `json:"owner,omitempty"`
	Environment string    `json:"environment,omitempty"`
	Tab         string    `json:"tab,omitempty"`
	Pane        string    `json:"pane,omitempty"`
	Since       time.Time `json:"since"`
}

// Floor is who holds a pane's input; Holder is empty once it is free.
type Floor struct {
	Pane        string    `json:"pane,omitempty"`
	Environment string    `json:"environment,omitempty"`
	Holder      string    `json:"holder,omitempty"`
	Since       time.Time `json:"since,omitzero"`
	// Until is when the hold lapses unless its holder types again.
	Until time.Time `json:"until,omitzero"`
}

// Presence is who is where, and who holds which pane's input, of what the
// credential sees (observe.presence).
type Presence struct {
	People []Present `json:"people"`
	Floors []Floor   `json:"floors"`
}

// PresenceSource reads the runtime's presence.
type PresenceSource interface {
	List() []presence.Entry
	Holds() []presence.Hold
}

// PresentView is entries as observers see them: by person and place,
// without connections or clients, each at its earliest.
func PresentView(entries []presence.Entry) []Present {
	out := make([]Present, 0, len(entries))
	seen := map[Present]int{}
	for _, e := range entries {
		p := Present{Name: e.Person.Name, Owner: e.Person.Owner(), Environment: e.Environment, Tab: e.Tab, Pane: e.Pane}
		if i, ok := seen[p]; ok {
			if e.Since.Before(out[i].Since) {
				out[i].Since = e.Since
			}
			continue
		}
		seen[p] = len(out)
		p.Since = e.Since
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Present) int {
		if a.Name != b.Name {
			if a.Name < b.Name {
				return -1
			}
			return 1
		}
		return a.Since.Compare(b.Since)
	})
	return out
}

// FloorView is a hold as observers see it.
func FloorView(h presence.Hold) Floor {
	return Floor{Pane: h.Pane, Environment: h.Environment, Holder: h.Holder.Name, Since: h.Since, Until: h.Until}
}

func (f Floor) target() Target { return Target{Environment: f.Environment, Pane: f.Pane} }

func (p Present) target() Target { return Target{Environment: p.Environment, Pane: p.Pane} }

// presence answers observe.presence: people where the credential sees
// them (one nowhere in particular only to a credential that sees all),
// and the holds of the panes it sees.
func (s *Server) presence(ctx context.Context, _ protocol.Empty) (Presence, error) {
	cred, err := sessionOf(ctx).credential()
	if err != nil {
		return Presence{}, err
	}
	out := Presence{People: []Present{}, Floors: []Floor{}}
	src := s.cfg.Sources.Presence
	if src == nil {
		return out, nil
	}
	for _, p := range PresentView(src.List()) {
		if cred.Scope.Sees(p.target()) {
			out.People = append(out.People, p)
		}
	}
	for _, h := range src.Holds() {
		if f := FloorView(h); cred.Scope.Sees(f.target()) {
			out.Floors = append(out.Floors, f)
		}
	}
	slices.SortFunc(out.Floors, func(a, b Floor) int {
		if a.Pane < b.Pane {
			return -1
		}
		if a.Pane > b.Pane {
			return 1
		}
		return 0
	})
	return out, nil
}

// translatePresence translates the runtime's presence events.
func translatePresence(ev []byte, typ string) (data any, target Target, err error) {
	switch typ {
	case EventPresenceChanged:
		var c struct {
			Presence []presence.Entry `json:"presence"`
		}
		err = json.Unmarshal(ev, &c)
		return map[string]any{"people": PresentView(c.Presence)}, Target{}, err
	default: // EventPaneFloor: a presence.Hold, or a free pane with no holder
		var h presence.Hold
		err = json.Unmarshal(ev, &h)
		f := FloorView(h)
		return f, f.target(), err
	}
}
