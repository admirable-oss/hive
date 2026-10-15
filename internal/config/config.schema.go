package config

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/notify"
)

// setter validates one raw TOML value and stores it.
type setter func(raw any) error

// schema maps section → key → setter, binding each key to its field in c.
// It is the single list of accepted keys; Render documents the same keys.
func (c *Config) schema() map[string]map[string]setter {
	return map[string]map[string]setter{
		"daemon": {
			"autostart":        boolean(&c.Daemon.Autostart),
			"shutdown_timeout": duration(&c.Daemon.ShutdownTimeout, time.Second, 10*time.Minute),
		},
		"log": {
			"level": func(raw any) error { return setString(raw, func(s string) error { return setLevel(&c.Log.Level, s) }) },
			"format": func(raw any) error {
				return setString(raw, func(s string) error { return setFormat(&c.Log.Format, s) })
			},
			"max_size_mb": integer(&c.Log.MaxSizeMB, 1, 1024),
			"max_backups": integer(&c.Log.MaxBackups, 0, 100),
		},
		"process": {
			"stop_grace": duration(&c.Process.StopGrace, 0, 5*time.Minute),
		},
		"terminal": {
			"default_width":  integer(&c.Terminal.DefaultWidth, 20, 1000),
			"default_height": integer(&c.Terminal.DefaultHeight, 5, 500),
			"scrollback_mb":  integer(&c.Terminal.ScrollbackMB, 0, 1024),
			"shell": func(raw any) error {
				return setString(raw, func(s string) error { c.Terminal.Shell = strings.TrimSpace(s); return nil })
			},
			"history_kb": deprecated("attaching now repaints the screen; set terminal.scrollback_mb for history instead"),
		},
		"git": {
			"refresh_interval": duration(&c.Git.RefreshInterval, time.Second, 10*time.Minute),
		},
		"session": {
			"restore":                  boolean(&c.Session.Restore),
			"resume_agents_on_restore": boolean(&c.Session.ResumeAgentsOnRestore),
			"restore_stagger":          duration(&c.Session.RestoreStagger, 0, time.Minute),
			"snapshot_interval":        duration(&c.Session.SnapshotInterval, 5*time.Second, time.Hour),
			"keep_snapshots":           integer(&c.Session.KeepSnapshots, 1, 1000),
			"replay_history":           boolean(&c.Session.ReplayHistory),
			"replay_lines":             integer(&c.Session.ReplayLines, 1, 100000),
		},
		"update": {
			"channel": func(raw any) error {
				return setString(raw, func(v string) error {
					v = strings.ToLower(strings.TrimSpace(v))
					if v != "stable" && v != "preview" {
						return fmt.Errorf("%q is not stable or preview", v)
					}
					c.Update.Channel = v
					return nil
				})
			},
		},
		"remote": {
			"ssh": func(raw any) error {
				return setString(raw, func(v string) error {
					if strings.TrimSpace(v) == "" {
						return fmt.Errorf("is empty")
					}
					c.Remote.SSH = strings.TrimSpace(v)
					return nil
				})
			},
			"frame_budget_kb": integer(&c.Remote.FrameBudgetKB, 0, 1<<20),
			"image_budget_kb": integer(&c.Remote.ImageBudgetKB, 0, 1<<20),
		},
		"images": {
			"max_kb":         integer(&c.Images.MaxKB, 1, 16<<10),
			"max_megapixels": integer(&c.Images.MaxMegapixels, 1, 64),
			"keep":           durationOrOff(&c.Images.Keep, time.Minute, 7*24*time.Hour),
		},
		"retention": {
			"exited_processes": duration(&c.Retention.ExitedProcesses, time.Hour, 10*365*24*time.Hour),
			"process_log_mb":   integer(&c.Retention.ProcessLogMB, 0, 100000),
			"gc_interval":      durationOrOff(&c.Retention.GCInterval, time.Minute, 30*24*time.Hour),
		},
		"ui": {
			"sidebar":       boolean(&c.UI.Sidebar),
			"sidebar_width": integer(&c.UI.SidebarWidth, 16, 120),
			"mouse":         boolean(&c.UI.Mouse),
			"clipboard": func(raw any) error {
				return setString(raw, func(s string) error {
					s = strings.ToLower(strings.TrimSpace(s))
					if !slices.Contains(clipboardModes, s) {
						return fmt.Errorf("%q is not one of %s", s, strings.Join(clipboardModes, ", "))
					}
					c.UI.Clipboard = s
					return nil
				})
			},
			"status_right": c.statusSchema,
		},
		"theme": {
			"name": func(raw any) error {
				return setString(raw, func(s string) error {
					if s = strings.ToLower(strings.TrimSpace(s)); s == "" {
						return fmt.Errorf("is empty")
					}
					c.Theme.Name = s
					return nil
				})
			},
			"custom": stringTable(&c.Theme.Custom),
		},
		"keys": c.keysSchema(),
		"notify": {
			"on":     stateList(&c.Notify.On),
			"toast":  boolean(&c.Notify.Toast),
			"system": boolean(&c.Notify.System),
			"sound":  boolean(&c.Notify.Sound),
			"terminal": func(raw any) error {
				return setString(raw, func(s string) error {
					s = strings.ToLower(strings.TrimSpace(s))
					if !slices.Contains(notify.TerminalModes, s) {
						return fmt.Errorf("%q is not one of %s", s, strings.Join(notify.TerminalModes, ", "))
					}
					c.Notify.Terminal = s
					return nil
				})
			},
			"agents": func(raw any) error {
				table, ok := raw.(map[string]any)
				if !ok {
					return typeError("a table of agent = [states]", raw)
				}
				out := map[string][]string{}
				for _, id := range slices.Sorted(maps.Keys(table)) {
					var states []string
					if err := stateList(&states)(table[id]); err != nil {
						return fmt.Errorf("%s %w", id, err)
					}
					out[id] = states
				}
				if len(out) == 0 {
					out = nil // an empty table sets nothing
				}
				c.Notify.Agents = out
				return nil
			},
		},
		"worktrees": {
			"directory": func(raw any) error {
				return setString(raw, func(s string) error {
					if s != "" && s != "~" && !strings.HasPrefix(s, "~/") && !filepath.IsAbs(s) {
						return fmt.Errorf("%q must be absolute or start with ~/", s)
					}
					c.Worktrees.Directory = s
					return nil
				})
			},
		},
	}
}

// keysSchema reads [keys]: prefix_keys, and a table per mode.
func (c *Config) keysSchema() map[string]setter {
	keys := map[string]setter{
		"command": c.keyCommandsSchema,
		"prefix_keys": func(raw any) error {
			list, err := keyList(raw)
			if err != nil {
				return err
			}
			if len(list) == 0 {
				return fmt.Errorf("needs at least one key")
			}
			c.Keys.Prefix = list
			return nil
		},
	}
	for _, mode := range KeyModes {
		keys[mode] = func(raw any) error {
			table, ok := raw.(map[string]any)
			if !ok && mode == "prefix" {
				return fmt.Errorf("is the table of prefix-mode bindings; set the prefix key itself with keys.prefix_keys")
			}
			if !ok {
				return typeError("a table of action = key(s)", raw)
			}
			bindings := map[string][]string{}
			var bad []string
			for _, action := range slices.Sorted(maps.Keys(table)) {
				list, err := keyList(table[action])
				if err != nil {
					bad = append(bad, action+" "+err.Error())
					continue
				}
				bindings[action] = list
			}
			if len(bindings) > 0 {
				if c.Keys.Modes == nil {
					c.Keys.Modes = map[string]map[string][]string{}
				}
				c.Keys.Modes[mode] = bindings
			}
			if len(bad) > 0 {
				return fmt.Errorf("%s (ignored)", strings.Join(bad, "; "))
			}
			return nil
		}
	}
	return keys
}

// keyList reads a key ("ctrl+a") or a list of keys; [] is an empty,
// non-nil list (unbind).
func keyList(raw any) ([]string, error) {
	switch v := raw.(type) {
	case string:
		return []string{v}, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, typeError("a key or a list of keys", item)
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, typeError("a key or a list of keys", raw)
}

// notifyStates are the agent states a notification may be raised for.
var notifyStates = []string{"blocked", "done", "working", "idle"}

// stateList reads a list of agent states; [] is an empty, non-nil list.
func stateList(dst *[]string) setter {
	return func(raw any) error {
		list, ok := raw.([]any)
		if !ok {
			return typeError("a list of states", raw)
		}
		out := []string{}
		for _, item := range list {
			s, ok := item.(string)
			if !ok || !slices.Contains(notifyStates, s) {
				return fmt.Errorf("%v is not one of %s", item, strings.Join(notifyStates, ", "))
			}
			out = append(out, s)
		}
		*dst = out
		return nil
	}
}

// stringTable reads a table of strings.
func stringTable(dst *map[string]string) setter {
	return func(raw any) error {
		table, ok := raw.(map[string]any)
		if !ok {
			return typeError("a table", raw)
		}
		out := map[string]string{}
		for k, v := range table {
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("%s %w", k, typeError("a string", v))
			}
			out[k] = s
		}
		if len(out) == 0 {
			out = nil // an empty table sets nothing
		}
		*dst = out
		return nil
	}
}

// apply sets every valid key in doc and returns a warning for each key it
// ignored. Sections and keys are visited in sorted order so warnings are
// deterministic.
func (c *Config) apply(doc map[string]any) []string {
	schema := c.schema()
	var warnings []string
	for _, section := range slices.Sorted(maps.Keys(doc)) {
		keys, known := schema[section]
		table, isTable := doc[section].(map[string]any)
		switch {
		case !known:
			warnings = append(warnings, fmt.Sprintf("unknown section or key %q ignored", section))
			continue
		case !isTable:
			warnings = append(warnings, fmt.Sprintf("%q must be a table; ignored", section))
			continue
		}
		for _, key := range slices.Sorted(maps.Keys(table)) {
			set, ok := keys[key]
			if !ok {
				warnings = append(warnings, fmt.Sprintf("unknown key %s.%s ignored", section, key))
				continue
			}
			if err := set(table[key]); err != nil {
				warnings = append(warnings, fmt.Sprintf("%s.%s: %v; using the default", section, key, err))
			}
		}
	}
	return warnings
}

// deprecated accepts a removed key with a warning instead of failing.
func deprecated(why string) setter {
	return func(any) error { return fmt.Errorf("is no longer used (%s)", why) }
}

func boolean(dst *bool) setter {
	return func(raw any) error {
		v, ok := raw.(bool)
		if !ok {
			return typeError("a boolean", raw)
		}
		*dst = v
		return nil
	}
}

func integer(dst *int, lo, hi int) setter {
	return func(raw any) error {
		v, ok := raw.(int64)
		if !ok {
			return typeError("an integer", raw)
		}
		if v < int64(lo) || v > int64(hi) {
			return fmt.Errorf("%d is outside %d..%d", v, lo, hi)
		}
		*dst = int(v)
		return nil
	}
}

func duration(dst *time.Duration, lo, hi time.Duration) setter {
	return func(raw any) error {
		return setString(raw, func(s string) error {
			d, err := time.ParseDuration(s)
			if err != nil {
				return fmt.Errorf("invalid duration %q (use e.g. \"500ms\", \"15s\", \"2m\")", s)
			}
			if d < lo || d > hi {
				return fmt.Errorf("%s is outside %s..%s", d, lo, hi)
			}
			*dst = d
			return nil
		})
	}
}

// durationOrOff is duration, but "0s" (or "0") turns the feature off.
func durationOrOff(dst *time.Duration, lo, hi time.Duration) setter {
	set := duration(dst, lo, hi)
	return func(raw any) error {
		if s, ok := raw.(string); ok && (s == "0" || s == "0s") {
			*dst = 0
			return nil
		}
		return set(raw)
	}
}

func setString(raw any, set func(string) error) error {
	s, ok := raw.(string)
	if !ok {
		return typeError("a string", raw)
	}
	return set(s)
}

func setLevel(dst *string, s string) error {
	if _, err := logging.ParseLevel(s); err != nil {
		return err
	}
	*dst = strings.ToLower(strings.TrimSpace(s))
	if *dst == "warning" {
		*dst = "warn"
	}
	return nil
}

func setFormat(dst *string, s string) error {
	f, err := logging.ParseFormat(s)
	if err != nil {
		return err
	}
	*dst = string(f)
	return nil
}

func typeError(want string, raw any) error {
	return fmt.Errorf("must be %s, got %T", want, raw)
}

// tables reads an array of tables ([[x]]).
func tables(raw any) ([]map[string]any, error) {
	switch v := raw.(type) {
	case []map[string]any:
		return v, nil
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			t, ok := item.(map[string]any)
			if !ok {
				return nil, typeError("an array of tables", item)
			}
			out = append(out, t)
		}
		return out, nil
	}
	return nil, typeError("an array of tables", raw)
}

// field reads one string field of a table entry; unknown fields are
// reported.
func stringField(t map[string]any, name string) (string, error) {
	raw, ok := t[name]
	if !ok {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s %w", name, typeError("a string", raw))
	}
	return s, nil
}

func unknownFields(t map[string]any, known ...string) error {
	for _, k := range slices.Sorted(maps.Keys(t)) {
		if !slices.Contains(known, k) {
			return fmt.Errorf("unknown field %q", k)
		}
	}
	return nil
}

// keyCommandsSchema reads [[keys.command]]; a broken entry is skipped with
// a warning, the rest apply.
func (c *Config) keyCommandsSchema(raw any) error {
	list, err := tables(raw)
	if err != nil {
		return err
	}
	var cmds []KeyCommand
	var bad []string
	for i, t := range list {
		kc, err := keyCommand(t)
		if err != nil {
			bad = append(bad, fmt.Sprintf("[%d]: %v", i, err))
			continue
		}
		cmds = append(cmds, kc)
	}
	c.Keys.Commands = cmds
	if len(bad) > 0 {
		return fmt.Errorf("%s (ignored)", strings.Join(bad, "; "))
	}
	return nil
}

func keyCommand(t map[string]any) (KeyCommand, error) {
	if err := unknownFields(t, "keys", "mode", "type", "command", "plugin", "action", "title"); err != nil {
		return KeyCommand{}, err
	}
	var kc KeyCommand
	raw, ok := t["keys"]
	if !ok {
		return kc, errors.New("keys is required")
	}
	keys, err := keyList(raw)
	if err != nil || len(keys) == 0 {
		return kc, errors.New("keys: a key or a list of keys")
	}
	kc.Keys = keys
	fields := map[string]*string{"mode": &kc.Mode, "type": &kc.Type, "command": &kc.Command, "plugin": &kc.Plugin, "action": &kc.Action, "title": &kc.Title}
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		if *fields[name], err = stringField(t, name); err != nil {
			return kc, err
		}
	}
	kc.Mode = strings.ToLower(strings.TrimSpace(kc.Mode))
	if kc.Mode == "" {
		kc.Mode = "prefix"
	}
	if !slices.Contains([]string{"prefix", "terminal", "navigate"}, kc.Mode) {
		return kc, fmt.Errorf("mode %q (want prefix, terminal or navigate)", kc.Mode)
	}
	kc.Type = strings.ToLower(strings.TrimSpace(kc.Type))
	switch kc.Type {
	case "popup", "pane", "shell":
		if strings.TrimSpace(kc.Command) == "" {
			return kc, fmt.Errorf("type %s needs a command", kc.Type)
		}
		if kc.Plugin != "" || kc.Action != "" {
			return kc, fmt.Errorf("plugin and action are for type plugin_action")
		}
	case "plugin_action":
		if kc.Plugin == "" || kc.Action == "" {
			return kc, errors.New("type plugin_action needs plugin and action")
		}
		if kc.Command != "" {
			return kc, errors.New("command is not for type plugin_action")
		}
	default:
		return kc, fmt.Errorf("type %q (want %s)", kc.Type, strings.Join(KeyCommandTypes, ", "))
	}
	return kc, nil
}

// statusSchema reads [[ui.status_right]].
func (c *Config) statusSchema(raw any) error {
	list, err := tables(raw)
	if err != nil {
		return err
	}
	var segs []StatusSegment
	var bad []string
	for i, t := range list {
		s, err := statusSegment(t)
		if err != nil {
			bad = append(bad, fmt.Sprintf("[%d]: %v", i, err))
			continue
		}
		segs = append(segs, s)
	}
	c.UI.StatusRight = segs
	if len(bad) > 0 {
		return fmt.Errorf("%s (ignored)", strings.Join(bad, "; "))
	}
	return nil
}

func statusSegment(t map[string]any) (StatusSegment, error) {
	if err := unknownFields(t, "type", "text", "format", "command", "interval"); err != nil {
		return StatusSegment{}, err
	}
	var s StatusSegment
	var interval string
	var err error
	fields := map[string]*string{"type": &s.Type, "text": &s.Text, "format": &s.Format, "command": &s.Command, "interval": &interval}
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		if *fields[name], err = stringField(t, name); err != nil {
			return s, err
		}
	}
	s.Type = strings.ToLower(strings.TrimSpace(s.Type))
	switch s.Type {
	case "text":
		if s.Text == "" {
			return s, errors.New("type text needs text")
		}
	case "datetime", "hostname":
	case "command":
		if strings.TrimSpace(s.Command) == "" {
			return s, errors.New("type command needs a command")
		}
		s.Interval = 30 * time.Second
		if interval != "" {
			d, err := time.ParseDuration(interval)
			if err != nil || d < time.Second || d > 24*time.Hour {
				return s, fmt.Errorf("interval %q: a duration from 1s to 24h", interval)
			}
			s.Interval = d
		}
	default:
		return s, fmt.Errorf("type %q (want %s)", s.Type, strings.Join(StatusTypes, ", "))
	}
	if s.Type != "command" && interval != "" {
		return s, errors.New("interval is for type command")
	}
	return s, nil
}

// KeyNames lists every accepted key as section.key (keys.<mode> tables and
// [[arrays]] by their names), sorted: the configuration's public surface.
func KeyNames() []string {
	var c Config
	var out []string
	for section, keys := range c.schema() {
		for key := range keys {
			out = append(out, section+"."+key)
		}
	}
	slices.Sort(out)
	return out
}
