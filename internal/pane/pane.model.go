package pane

import (
	"time"

	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/process"
)

// Tab is one layout of panes in an environment.
type Tab struct {
	ID            string       `json:"id"`
	EnvironmentID string       `json:"environment_id"`
	Name          string       `json:"name"`
	Layout        *layout.Node `json:"layout,omitempty"`
	Popups        []Popup      `json:"popups,omitempty"`
	// Focused is the pane that receives input; Zoomed, when set, fills the tab.
	Focused string `json:"focused,omitempty"`
	Zoomed  string `json:"zoomed,omitempty"`
	// Width and Height are the tab's area, which pane sizes derive from.
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	CreatedAt time.Time `json:"created_at"`

	// Active is set on reads for the environment's active tab; not stored.
	Active bool `json:"active,omitempty"`
}

// Popup is a pane floating over a tab, centred, sized in percent of it.
type Popup struct {
	Pane      string `json:"pane"`
	WidthPct  int    `json:"width_pct"`
	HeightPct int    `json:"height_pct"`
}

// Pane is a place in a tab showing one terminal process.
type Pane struct {
	ID            string    `json:"id"`
	TabID         string    `json:"tab_id"`
	EnvironmentID string    `json:"environment_id"`
	ProcessID     string    `json:"process_id"`
	Name          string    `json:"name,omitempty"`
	CreatedAt     time.Time `json:"created_at"`

	// Filled in on reads, not stored.
	Process *process.Process `json:"process,omitempty"`
	Rect    *layout.Rect     `json:"rect,omitempty"` // nil when not visible (another pane is zoomed)
	Focused bool             `json:"focused,omitempty"`
	Zoomed  bool             `json:"zoomed,omitempty"`
	Popup   bool             `json:"popup,omitempty"`
}

// Spec describes what a new pane runs.
type Spec struct {
	Name string `json:"name,omitempty"`
	// Command is the program and its arguments; empty runs the shell.
	Command []string          `json:"command,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// State is everything stored for one environment.
type State struct {
	Schema    int    `json:"schema"`
	ActiveTab string `json:"active_tab,omitempty"`
	Tabs      []Tab  `json:"tabs"`
	Panes     []Pane `json:"panes"`
}

const stateSchema = 1

// Request types of the pane API.
type (
	CreateTabRequest struct {
		EnvironmentID string `json:"environment_id"`
		Name          string `json:"name,omitempty"`
		Pane          Spec   `json:"pane,omitempty"`
	}
	SplitRequest struct {
		// Pane is the pane to split; with only TabID, the tab's focused pane.
		Pane      string           `json:"pane,omitempty"`
		TabID     string           `json:"tab_id,omitempty"`
		Direction layout.Direction `json:"direction,omitempty"` // default right
		// Ratio is the new pane's share of the split area (default 0.5).
		Ratio float64 `json:"ratio,omitempty"`
		Spec  Spec    `json:"spec,omitempty"`
		// Focus moves the focus to the new pane (default true).
		Focus *bool `json:"focus,omitempty"`
	}
	MoveRequest struct {
		Pane string `json:"pane"`
		// TabID is the destination tab; empty with EnvironmentID set makes
		// a new tab there.
		TabID         string `json:"tab_id,omitempty"`
		EnvironmentID string `json:"environment_id,omitempty"`
		// Target and Direction place the pane next to a pane of the
		// destination (default: the focused pane, to its right).
		Target    string           `json:"target,omitempty"`
		Direction layout.Direction `json:"direction,omitempty"`
	}
	PopupRequest struct {
		TabID     string `json:"tab_id"`
		Spec      Spec   `json:"spec,omitempty"`
		WidthPct  int    `json:"width_pct,omitempty"`  // default 80
		HeightPct int    `json:"height_pct,omitempty"` // default 80
	}
)
