// Package integration installs hooks into coding agents' own settings so
// they report their state to Hive (`hive pane report-agent --hook`), more
// precisely than Hive can read it from their screens, along with their
// session IDs for resuming them.
//
// Every change is idempotent and reversible: the original settings file is
// backed up before the first change, Hive's hooks are recognised by their
// command and removed without touching anything else, and when nothing else
// changed since, uninstalling puts the original bytes back.
package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Marker is in every hook command Hive installs; it is how they are found
// again.
const Marker = "pane report-agent --hook"

// ErrUnknown means no integration has that name.
var ErrUnknown = errors.New("integration: unknown agent")

// Integration is one agent whose settings can carry Hive's hooks.
type Integration struct {
	ID   string // the agent manifest's ID: claude, codex
	Name string
	// Path is the settings file the hooks go in.
	Path  string
	hooks []hook
}

// hook is one agent event and the state it reports.
type hook struct {
	Event   string
	Matcher string // "" for events that take none
	State   string
	TTL     string
}

// command is the shell command a hook runs. Outside a Hive pane HIVE_BIN
// is unset and it does nothing; it never fails the agent's hook.
func (h hook) command() string {
	return fmt.Sprintf(`[ -z "$HIVE_BIN" ] || "$HIVE_BIN" %s --state %s --ttl %s || true`, Marker, h.State, h.TTL)
}

// Reports hold for a minute: the agent's screen decides again after, which
// covers events the agents send no hook for (an interrupted turn, a
// rejected permission).
const ttl = "1m"

// All returns the integrations, with their settings files found from the
// environment (CLAUDE_CONFIG_DIR, CODEX_HOME) and the home directory.
func All(home string, getenv func(string) string) []Integration {
	dir := func(env, def string) string {
		if d := getenv(env); d != "" {
			return d
		}
		return filepath.Join(home, def)
	}
	return []Integration{
		{
			ID: "claude", Name: "Claude Code",
			Path: filepath.Join(dir("CLAUDE_CONFIG_DIR", ".claude"), "settings.json"),
			hooks: []hook{
				{Event: "SessionStart", State: "idle", TTL: ttl},
				{Event: "UserPromptSubmit", State: "working", TTL: ttl},
				{Event: "PreToolUse", Matcher: "*", State: "working", TTL: ttl},
				{Event: "PostToolUse", Matcher: "*", State: "working", TTL: ttl},
				{Event: "Notification", Matcher: "permission_prompt", State: "blocked", TTL: ttl},
				{Event: "Stop", State: "idle", TTL: ttl},
			},
		},
		{
			ID: "codex", Name: "Codex",
			Path: filepath.Join(dir("CODEX_HOME", ".codex"), "hooks.json"),
			hooks: []hook{
				{Event: "SessionStart", State: "idle", TTL: ttl},
				{Event: "UserPromptSubmit", State: "working", TTL: ttl},
				{Event: "PreToolUse", Matcher: "*", State: "working", TTL: ttl},
				{Event: "PostToolUse", Matcher: "*", State: "working", TTL: ttl},
				{Event: "Stop", State: "idle", TTL: ttl},
			},
		},
	}
}

// Find returns the integration for an agent.
func Find(all []Integration, id string) (Integration, error) {
	for _, in := range all {
		if in.ID == id {
			return in, nil
		}
	}
	var ids []string
	for _, in := range all {
		ids = append(ids, in.ID)
	}
	return Integration{}, fmt.Errorf("%w %q (have %s)", ErrUnknown, id, strings.Join(ids, ", "))
}

// Backup is where the original settings file is kept.
func (in Integration) Backup() string { return in.Path + ".hive-backup" }

// State says how much of an integration is installed.
type State string

const (
	NotInstalled State = "not installed"
	Installed    State = "installed"
	Partial      State = "partly installed" // some hooks are missing or changed
)

// Status is what Status found.
type Status struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	State State  `json:"state"`
	// Hooks counts Hive's hooks found, of Want.
	Hooks int `json:"hooks"`
	Want  int `json:"want"`
	// Backup is the original file's copy, when there is one.
	Backup string `json:"backup,omitempty"`
}

// Status reports whether the hooks are installed.
func (in Integration) Status() (Status, error) {
	st := Status{ID: in.ID, Name: in.Name, Path: in.Path, Want: len(in.hooks), State: NotInstalled}
	if _, err := os.Stat(in.Backup()); err == nil {
		st.Backup = in.Backup()
	}
	doc, _, err := read(in.Path)
	if err != nil {
		return st, err
	}
	hooks, _ := doc["hooks"].(map[string]any)
	for _, h := range in.hooks {
		if hasCommand(hooks[h.Event], h.Matcher, h.command()) {
			st.Hooks++
		}
	}
	found := countMarked(hooks)
	switch {
	case st.Hooks == st.Want && found == st.Want:
		st.State = Installed
	case found > 0:
		st.State = Partial
	}
	return st, nil
}

// Install adds the hooks, replacing any older Hive hooks. It reports
// whether the file changed.
func (in Integration) Install() (bool, error) {
	doc, orig, err := read(in.Path)
	if err != nil {
		return false, err
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if doc["hooks"] != nil && hooks == nil {
		return false, fmt.Errorf("%s: \"hooks\" is not an object; fix it by hand first", in.Path)
	}
	if hooks == nil {
		hooks = map[string]any{}
	}
	removeMarked(hooks)
	for _, h := range in.hooks {
		entry := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": h.command()}}}
		if h.Matcher != "" {
			entry["matcher"] = h.Matcher
		}
		list, _ := hooks[h.Event].([]any)
		hooks[h.Event] = append(list, entry)
	}
	doc["hooks"] = hooks
	out, err := encode(doc)
	if err != nil {
		return false, err
	}
	if orig != nil && bytes.Equal(out, orig) {
		return false, nil
	}
	if orig != nil {
		if _, err := os.Stat(in.Backup()); errors.Is(err, fs.ErrNotExist) {
			if err := writeFile(in.Backup(), orig); err != nil {
				return false, err
			}
		}
	}
	return true, writeFile(in.Path, out)
}

// Uninstall removes Hive's hooks and nothing else. When the result is the
// backed-up original, the original's bytes are restored and the backup
// removed. It reports whether the file changed.
func (in Integration) Uninstall() (bool, error) {
	doc, orig, err := read(in.Path)
	if err != nil || orig == nil {
		return false, err
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if countMarked(hooks) == 0 {
		return false, nil
	}
	removeMarked(hooks)
	if len(hooks) == 0 {
		delete(doc, "hooks")
	}

	if backup, err := os.ReadFile(in.Backup()); err == nil {
		var was map[string]any
		if json.Unmarshal(backup, &was) == nil && equalJSON(was, doc) {
			if err := writeFile(in.Path, backup); err != nil {
				return false, err
			}
			return true, os.Remove(in.Backup())
		}
	}
	if len(doc) == 0 {
		if _, err := os.Stat(in.Backup()); errors.Is(err, fs.ErrNotExist) {
			return true, os.Remove(in.Path) // Hive created it
		}
	}
	out, err := encode(doc)
	if err != nil {
		return false, err
	}
	return true, writeFile(in.Path, out)
}

// read decodes a JSON settings file; a missing one is empty, with nil
// bytes.
func read(path string) (map[string]any, []byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	doc := map[string]any{}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, nil, fmt.Errorf("%s: %w (fix it by hand first)", path, err)
		}
	}
	return doc, data, nil
}

func encode(doc map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeFile replaces path atomically, keeping its mode.
func writeFile(path string, data []byte) error {
	mode := fs.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(mode)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func equalJSON(a, b any) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}

// entryCommands returns the commands of one event entry
// ({"matcher": …, "hooks": [{"type": "command", "command": …}]}).
func entryCommands(entry any) []string {
	e, _ := entry.(map[string]any)
	list, _ := e["hooks"].([]any)
	var out []string
	for _, h := range list {
		if m, ok := h.(map[string]any); ok {
			if c, ok := m["command"].(string); ok {
				out = append(out, c)
			}
		}
	}
	return out
}

func hasCommand(entries any, matcher, command string) bool {
	list, _ := entries.([]any)
	for _, entry := range list {
		e, _ := entry.(map[string]any)
		m, _ := e["matcher"].(string)
		if m == matcher && slices.Contains(entryCommands(entry), command) {
			return true
		}
	}
	return false
}

func marked(c string) bool { return strings.Contains(c, Marker) }

func countMarked(hooks map[string]any) int {
	n := 0
	for _, entries := range hooks {
		list, _ := entries.([]any)
		for _, entry := range list {
			for _, c := range entryCommands(entry) {
				if marked(c) {
					n++
				}
			}
		}
	}
	return n
}

// removeMarked deletes Hive's hook commands, then the entries and events
// left empty by that.
func removeMarked(hooks map[string]any) {
	for event, entries := range hooks {
		list, ok := entries.([]any)
		if !ok {
			continue
		}
		var keep []any
		for _, entry := range list {
			e, ok := entry.(map[string]any)
			inner, _ := e["hooks"].([]any)
			if !ok || inner == nil {
				keep = append(keep, entry)
				continue
			}
			var rest []any
			for _, h := range inner {
				m, _ := h.(map[string]any)
				if c, _ := m["command"].(string); marked(c) {
					continue
				}
				rest = append(rest, h)
			}
			if len(rest) == 0 {
				continue
			}
			e["hooks"] = rest
			keep = append(keep, e)
		}
		if len(keep) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = keep
		}
	}
}
