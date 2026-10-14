package pluginsdk

import (
	"encoding/json"
	"fmt"
	"io"
)

// Context describes why Hive invoked a plugin command.
type Context struct {
	Plugin      string `json:"plugin"`
	Trigger     string `json:"trigger"`
	Action      string `json:"action,omitempty"`
	Event       *Event `json:"event,omitempty"`
	Session     string `json:"session"`
	Environment string `json:"environment,omitempty"`
	Tab         string `json:"tab,omitempty"`
	Pane        string `json:"pane,omitempty"`
}

// Event is the event that caused this invocation.
type Event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
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
