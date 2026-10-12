package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// HerdrImport is what ImportHerdr did with a herdr configuration.
type HerdrImport struct {
	// Mapped says, per herdr key, what Hive setting it became.
	Mapped []string `json:"mapped"`
	// Skipped says, per herdr key, why it has no Hive equivalent.
	Skipped []string `json:"skipped"`
}

// HerdrDefaultKeys is herdr's default keymap (its config reference), for
// users who want their muscle memory in Hive: herdr action → binding.
var HerdrDefaultKeys = map[string]string{
	"prefix": "ctrl+b", "help": "prefix+?", "goto": "prefix+g", "workspace_picker": "prefix+w",
	"navigate_pane_left": "h", "navigate_pane_down": "j", "navigate_pane_up": "k", "navigate_pane_right": "l",
	"detach": "prefix+q", "open_notification_target": "prefix+o",
	"new_tab": "prefix+c", "rename_tab": "prefix+shift+t", "previous_tab": "prefix+p", "next_tab": "prefix+n",
	"switch_tab": "prefix+1..9", "close_tab": "prefix+shift+x", "rename_pane": "prefix+shift+p",
	"edit_scrollback": "prefix+e", "copy_mode": "prefix+[",
	"focus_pane_left": "prefix+h", "focus_pane_down": "prefix+j", "focus_pane_up": "prefix+k", "focus_pane_right": "prefix+l",
	"swap_pane_left": "prefix+shift+h", "swap_pane_right": "prefix+shift+l",
	"cycle_pane_next": "prefix+tab", "cycle_pane_previous": "prefix+shift+tab",
	"split_vertical": "prefix+v", "split_horizontal": "prefix+minus", "close_pane": "prefix+x",
	"zoom": "prefix+z", "resize_mode": "prefix+r", "toggle_sidebar": "prefix+b",
}

// herdrActions maps herdr's key actions to Hive's (keymap names, with
// underscores as config files spell them). Navigate-mode keys bind in
// Hive's navigate mode.
var herdrActions = map[string]string{
	"help": "help", "goto": "goto", "workspace_picker": "goto",
	"detach": "detach", "open_notification_target": "open_notification_target",
	"next_agent":         "next_agent",
	"previous_workspace": "prev_env", "next_workspace": "next_env",
	"new_tab": "new_tab", "rename_tab": "rename_tab", "previous_tab": "prev_tab", "next_tab": "next_tab",
	"close_tab": "close_tab", "rename_pane": "rename_pane", "edit_scrollback": "edit_scrollback",
	"copy_mode":       "mode_copy",
	"focus_pane_left": "focus_left", "focus_pane_down": "focus_down", "focus_pane_up": "focus_up", "focus_pane_right": "focus_right",
	"navigate_pane_left": "focus_left", "navigate_pane_down": "focus_down", "navigate_pane_up": "focus_up", "navigate_pane_right": "focus_right",
	"swap_pane_right": "swap_next", "swap_pane_left": "swap_prev",
	"cycle_pane_next": "focus_next", "cycle_pane_previous": "focus_prev",
	"split_vertical": "split_right", "split_horizontal": "split_down", "close_pane": "close_pane",
	"zoom": "zoom", "resize_mode": "mode_resize", "toggle_sidebar": "toggle_sidebar",
	"resize_pane_left": "resize_left", "resize_pane_down": "resize_down", "resize_pane_up": "resize_up", "resize_pane_right": "resize_right",
}

// herdrNoEquivalent explains herdr keys Hive has nothing for.
var herdrNoEquivalent = map[string]string{
	"keys.settings":                "Hive has no settings screen (edit config.toml; `hive config validate`)",
	"keys.new_workspace":           "environments are made with `hive env create`",
	"keys.rename_workspace":        "environments are not renamed",
	"keys.close_workspace":         "environments are removed with `hive env remove`",
	"keys.new_worktree":            "worktrees are made with `hive worktree create`",
	"keys.open_worktree":           "worktrees are opened with `hive worktree open`",
	"keys.remove_worktree":         "worktrees are removed with `hive worktree remove`",
	"keys.navigate_workspace_up":   "the sidebar lists environments; prev_env/next_env move between them",
	"keys.navigate_workspace_down": "the sidebar lists environments; prev_env/next_env move between them",
	"keys.reload_config":           "the UI reads the config when it opens",
	"keys.previous_agent":          "Hive has next_agent (the next agent that wants you)",
	"keys.focus_agent":             "Hive has focus_agent_1 … focus_agent_9",
	"keys.remote_image_paste":      "the image paste bridge is not in Hive yet",
	"keys.move_tab_previous":       "tabs are not reordered",
	"keys.move_tab_next":           "tabs are not reordered",
	"keys.switch_workspace":        "use prev_env/next_env or the goto picker",
	"keys.clear_pane":              "send clear to the program instead",
	"keys.swap_pane_up":            "Hive swaps with the next or previous pane only",
	"keys.swap_pane_down":          "Hive swaps with the next or previous pane only",
	"keys.last_pane":               "no equivalent",
	"keys.indexed":                 "Hive binds tab_1 … tab_9 and focus_agent_1 … focus_agent_9 by default",
	"ui.copy_on_select":            "Hive always copies a mouse selection",
	"terminal.shell_mode":          "set terminal.shell to the command line you want (e.g. \"zsh -l\")",
	"terminal.new_cwd":             "new panes start where the pane they come from is working",
	"terminal.kitty_graphics":      "Kitty graphics come after 1.0",
	"server.headless_cols":         "set terminal.default_width",
	"server.headless_rows":         "set terminal.default_height",
	"onboarding":                   "Hive has no onboarding screen",
	"theme.dark_name":              "theme.name = \"auto\" picks Hive's dark or light theme",
	"theme.light_name":             "theme.name = \"auto\" picks Hive's dark or light theme",
	"ui.tab_bar_position":          "the tab bar is always at the top",
	"ui.tab_bar_right_separator":   "segments are separated by two spaces",
	"ui.pane_borders":              "pane borders are always drawn",
	"ui.pane_scrollbars":           "Hive shows no scrollbars",
	"ui.toast.delay_seconds":       "toasts show at once",
	"ui.toast.herdr":               "toasts show in the UI's corner",
	"ui.toast.clipboard":           "copying shows a toast",
	"ui.sound.path":                "Hive plays the system's sound",
	"ui.sound.done_path":           "Hive plays the system's sound",
	"ui.sound.request_path":        "Hive plays the system's sound",
	"ui.sidebar":                   "the sidebar's agent rows are not configurable",
	"experimental":                 "no equivalent",
	"session.pane_history":         "set session.replay_history",
	"theme.custom.light":           "custom themes have one palette in Hive",
	"theme.custom.dark":            "custom themes have one palette in Hive",
	"ui.sound.agents":              "notify.agents sets per-agent notifications",
}

// herdrColours maps herdr's theme tokens to Hive's.
var herdrColours = map[string]string{
	"accent": "accent", "text": "fg", "panel_bg": "bg", "selection_bg": "selection",
	"green": "success", "yellow": "warning", "red": "error", "blue": "info",
	"overlay0": "muted", "surface1": "border",
}

// ImportHerdr applies a herdr config.toml to cfg: every setting with a
// Hive equivalent is set, the rest are listed with the reason. With
// herdrKeys, herdr's default keymap applies first, so its keys work in Hive.
func ImportHerdr(cfg Config, data []byte, herdrKeys bool) (Config, HerdrImport, error) {
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return cfg, HerdrImport{}, fmt.Errorf("read the herdr config: %w", err)
	}
	im := &importer{cfg: cfg}
	keys, _ := doc["keys"].(map[string]any)
	if herdrKeys {
		merged := map[string]any{}
		for k, v := range HerdrDefaultKeys {
			merged[k] = v
		}
		maps.Copy(merged, keys)
		keys = merged
	}
	im.keys(keys)
	im.rest(doc, "")
	slices.Sort(im.out.Mapped)
	slices.Sort(im.out.Skipped)
	return im.cfg, im.out, nil
}

type importer struct {
	cfg Config
	out HerdrImport
}

func (im *importer) mapped(from, to string) {
	im.out.Mapped = append(im.out.Mapped, from+" → "+to)
}

func (im *importer) skip(from, why string) {
	im.out.Skipped = append(im.out.Skipped, from+": "+why)
}

// herdrKey turns a herdr key ("prefix+shift+n", "ctrl+alt+n", "minus") into
// Hive's mode and key.
func herdrKey(s string) (mode, key string) {
	mode = "terminal"
	if rest, ok := strings.CutPrefix(s, "prefix+"); ok {
		mode, s = "prefix", rest
	}
	parts := strings.Split(s, "+")
	switch last := parts[len(parts)-1]; last {
	case "minus":
		parts[len(parts)-1] = "-"
	case "plus":
		parts[len(parts)-1] = "+"
	}
	return mode, strings.Join(parts, "+")
}

func (im *importer) bind(mode, action, key string) {
	if im.cfg.Keys.Modes == nil {
		im.cfg.Keys.Modes = map[string]map[string][]string{}
	}
	if im.cfg.Keys.Modes[mode] == nil {
		im.cfg.Keys.Modes[mode] = map[string][]string{}
	}
	if !slices.Contains(im.cfg.Keys.Modes[mode][action], key) {
		im.cfg.Keys.Modes[mode][action] = append(im.cfg.Keys.Modes[mode][action], key)
	}
}

func (im *importer) keys(keys map[string]any) {
	for _, name := range slices.Sorted(maps.Keys(keys)) {
		raw := keys[name]
		from := "keys." + name
		switch name {
		case "prefix":
			if s, ok := raw.(string); ok && s != "" {
				im.cfg.Keys.Prefix = []string{s}
				im.mapped(from, "keys.prefix_keys = "+tomlList(im.cfg.Keys.Prefix))
			}
			continue
		case "command":
			im.commands(raw)
			continue
		case "switch_tab":
			s, _ := raw.(string)
			mode, key := herdrKey(s)
			if prefix, ok := strings.CutSuffix(key, "1..9"); ok {
				for n := 1; n <= 9; n++ {
					im.bind(mode, fmt.Sprintf("tab_%d", n), fmt.Sprint(prefix, n))
				}
				im.mapped(from, fmt.Sprintf("keys.%s.tab_1 … tab_9", mode))
				continue
			}
			im.skip(from, "only a 1..9 range is understood")
			continue
		}
		if why, ok := herdrNoEquivalent[from]; ok {
			im.skip(from, why)
			continue
		}
		action, ok := herdrActions[name]
		if !ok {
			im.skip(from, "unknown herdr action")
			continue
		}
		var bindings []string
		switch v := raw.(type) {
		case string:
			bindings = []string{v}
		case []any:
			for _, x := range v {
				if s, ok := x.(string); ok {
					bindings = append(bindings, s)
				}
			}
		}
		for _, b := range bindings {
			if b == "" {
				continue
			}
			mode, key := herdrKey(b)
			if strings.HasPrefix(name, "navigate_") {
				mode = "navigate" // herdr's navigate mode: Hive's too
			}
			im.bind(mode, action, key)
			im.mapped(from, fmt.Sprintf("keys.%s.%s = %q", mode, action, key))
		}
	}
}

func (im *importer) commands(raw any) {
	list, err := tables(raw)
	if err != nil {
		im.skip("keys.command", "not an array of tables")
		return
	}
	for i, t := range list {
		from := fmt.Sprintf("keys.command[%d]", i)
		key, _ := t["key"].(string)
		run, _ := t["run"].(string)
		if run == "" {
			run, _ = t["command"].(string)
		}
		if key == "" || run == "" {
			im.skip(from, "needs a key and a command")
			continue
		}
		kind, _ := t["type"].(string)
		typ := ""
		switch strings.ToLower(kind) {
		case "", "popup", "float", "floating", "temporary", "temp", "overlay":
			typ = "popup"
		case "pane", "split", "tab":
			typ = "pane"
		case "detached", "background", "shell", "helper":
			typ = "shell"
		default:
			im.skip(from, fmt.Sprintf("type %q has no Hive equivalent", kind))
			continue
		}
		if cwd, _ := t["cwd"].(string); cwd != "" {
			run = "cd " + shellQuote(cwd) + " && " + run
		}
		mode, k := herdrKey(key)
		im.cfg.Keys.Commands = append(im.cfg.Keys.Commands, KeyCommand{Keys: []string{k}, Mode: mode, Type: typ, Command: run})
		im.mapped(from, fmt.Sprintf("[[keys.command]] %s %s %q", typ, k, run))
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// rest maps every setting outside [keys], walking nested tables.
func (im *importer) rest(doc map[string]any, prefix string) {
	for _, k := range slices.Sorted(maps.Keys(doc)) {
		name := prefix + k
		if name == "keys" {
			continue
		}
		v := doc[k]
		if why, ok := herdrNoEquivalent[name]; ok {
			im.skip(name, why)
			continue
		}
		if t, ok := v.(map[string]any); ok && name != "theme.custom" {
			im.rest(t, name+".")
			continue
		}
		im.setting(name, v)
	}
}

func (im *importer) setting(name string, v any) {
	b, isBool := v.(bool)
	s, isStr := v.(string)
	n, isInt := v.(int64)
	c := &im.cfg
	switch {
	case name == "theme.name" && isStr:
		theme := map[string]string{"tokyonight": "tokyo-night", "catppuccin-mocha": "catppuccin"}[s]
		if theme == "" {
			theme = s
		}
		if !slices.Contains([]string{"auto", "catppuccin", "catppuccin-latte", "tokyo-night", "gruvbox", "nord", "terminal"}, theme) {
			im.skip(name, fmt.Sprintf("Hive has no theme %q", s))
			return
		}
		if c.Theme.Name != "custom" {
			c.Theme.Name = theme
		}
		if c.Theme.Custom != nil {
			c.Theme.Custom["base"] = theme
		}
		im.mapped(name, fmt.Sprintf("theme.name = %q", theme))
	case name == "theme.auto_switch" && isBool:
		if b && c.Theme.Name != "custom" {
			c.Theme.Name = "auto"
			im.mapped(name, `theme.name = "auto"`)
		}
	case name == "theme.custom":
		t, _ := v.(map[string]any)
		custom := map[string]string{"base": c.Theme.Name}
		if custom["base"] == "custom" || custom["base"] == "auto" {
			custom["base"] = "catppuccin"
		}
		for _, token := range slices.Sorted(maps.Keys(t)) {
			if token == "light" || token == "dark" {
				im.skip(name+"."+token, herdrNoEquivalent["theme.custom."+token])
				continue
			}
			col, ok := t[token].(string)
			hive := herdrColours[token]
			if !ok || hive == "" {
				im.skip(name+"."+token, "Hive's custom theme has no such colour")
				continue
			}
			custom[hive] = col
			im.mapped(name+"."+token, "theme.custom."+hive)
		}
		if len(custom) > 1 {
			c.Theme.Name, c.Theme.Custom = "custom", custom
		}
	case name == "ui.sidebar_width" && isInt:
		c.UI.SidebarWidth = min(max(int(n), 16), 120)
		im.mapped(name, fmt.Sprintf("ui.sidebar_width = %d", c.UI.SidebarWidth))
	case name == "ui.sidebar_start_collapsed" && isBool:
		c.UI.Sidebar = !b
		im.mapped(name, fmt.Sprintf("ui.sidebar = %v", c.UI.Sidebar))
	case name == "ui.mouse_capture" && isBool:
		c.UI.Mouse = b
		im.mapped(name, fmt.Sprintf("ui.mouse = %v", b))
	case name == "ui.toast.delivery" && isStr:
		switch s {
		case "off":
			c.Notify.Toast, c.Notify.System, c.Notify.Terminal = false, false, "off"
		case "herdr":
			c.Notify.Toast = true
		case "terminal":
			c.Notify.Terminal = "auto"
		case "system":
			c.Notify.System = true
		default:
			im.skip(name, fmt.Sprintf("unknown delivery %q", s))
			return
		}
		im.mapped(name, "notify.toast/terminal/system")
	case name == "ui.sound.enabled" && isBool:
		c.Notify.Sound = b
		im.mapped(name, fmt.Sprintf("notify.sound = %v", b))
	case name == "ui.tab_bar_right":
		im.tabBar(v)
	case name == "terminal.default_shell" && isStr:
		c.Terminal.Shell = strings.TrimSpace(s)
		im.mapped(name, fmt.Sprintf("terminal.shell = %q", c.Terminal.Shell))
	case name == "session.resume_agents_on_restore" && isBool:
		c.Session.ResumeAgentsOnRestore = b
		im.mapped(name, fmt.Sprintf("session.resume_agents_on_restore = %v", b))
	case name == "session.startup_per_agent_delay_ms" && isInt:
		c.Session.RestoreStagger = min(time.Duration(max(n, 0))*time.Millisecond, time.Minute)
		im.mapped(name, fmt.Sprintf("session.restore_stagger = %q", c.Session.RestoreStagger))
	case strings.HasPrefix(name, "ui.sound.") && !strings.HasSuffix(name, "path"):
		im.skip(name, herdrNoEquivalent["ui.sound.agents"])
	default:
		im.skip(name, "no Hive equivalent")
	}
}

// tabBar maps ui.tab_bar_right entries to status segments.
func (im *importer) tabBar(v any) {
	list, err := tables(v)
	if err != nil {
		im.skip("ui.tab_bar_right", "not a list of entries")
		return
	}
	for i, t := range list {
		from := fmt.Sprintf("ui.tab_bar_right[%d]", i)
		typ, _ := t["type"].(string)
		seg := StatusSegment{Type: typ}
		switch typ {
		case "hostname":
		case "text":
			seg.Text, _ = t["text"].(string)
		case "datetime":
			seg.Format, _ = t["format"].(string)
		case "command":
			seg.Command, _ = t["command"].(string)
			if seg.Command == "" {
				seg.Command, _ = t["run"].(string)
			}
			seg.Interval = 30 * time.Second
			if secs, ok := t["interval_seconds"].(int64); ok && secs > 0 {
				seg.Interval = time.Duration(secs) * time.Second
			}
			if s, ok := t["interval"].(string); ok {
				if d, err := time.ParseDuration(s); err == nil && d >= time.Second {
					seg.Interval = d
				}
			}
		default:
			im.skip(from, fmt.Sprintf("type %q has no Hive equivalent", typ))
			continue
		}
		if (seg.Type == "text" && seg.Text == "") || (seg.Type == "command" && seg.Command == "") {
			im.skip(from, "empty")
			continue
		}
		im.cfg.UI.StatusRight = append(im.cfg.UI.StatusRight, seg)
		im.mapped(from, "[[ui.status_right]] "+seg.Type)
	}
}
