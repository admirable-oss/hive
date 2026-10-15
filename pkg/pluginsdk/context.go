package pluginsdk

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"time"
)

// Context describes why Hive invoked a plugin command. Its shape is part
// of the plugin API: within a version fields are only ever added.
type Context struct {
	// API is the plugin API version Hive ran the plugin under (0 from a
	// hive older than versioned plugin APIs).
	API         int    `json:"api,omitempty"`
	Plugin      string `json:"plugin"`
	Trigger     string `json:"trigger"`
	Action      string `json:"action,omitempty"`
	Run         string `json:"run,omitempty"`
	Event       *Event `json:"event,omitempty"`
	Session     string `json:"session"`
	Environment string `json:"environment,omitempty"`
	Tab         string `json:"tab,omitempty"`
	Pane        string `json:"pane,omitempty"`
	// Capabilities are what the plugin holds now.
	Capabilities []string `json:"capabilities,omitempty"`
}

// Has reports whether the plugin holds capability now.
func (c Context) Has(capability string) bool {
	return slices.Contains(c.Capabilities, capability)
}

// Event is the event that caused this invocation.
type Event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
	// Seq orders events; Missed says some were lost before this one, so
	// re-read whatever state the plugin follows.
	Seq    uint64    `json:"seq,omitempty"`
	Time   time.Time `json:"time,omitzero"`
	Missed bool      `json:"missed,omitempty"`
}

// ReadContext reads and validates the JSON invocation context Hive writes to
// a plugin's standard input. Input is limited to 1 MiB, matching protocol frames.
func ReadContext(r io.Reader) (Context, error) {
	var c Context
	data, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil {
		return Context{}, fmt.Errorf("read Hive plugin context: %w", err)
	}
	if len(data) > 1<<20 {
		return Context{}, fmt.Errorf("read Hive plugin context: context exceeds 1 MiB")
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return Context{}, fmt.Errorf("read Hive plugin context: %w", err)
	}
	if c.Plugin == "" || c.Trigger == "" || c.Session == "" {
		return Context{}, fmt.Errorf("read Hive plugin context: plugin, trigger and session are required")
	}
	return c, nil
}
