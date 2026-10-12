package config_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/config"
)

const herdrSample = `
onboarding = false

[theme]
name = "tokyonight"

[theme.custom]
accent = "#ff79c6"
text = "#eeeeee"
surface_dim = "#000000"

[terminal]
default_shell = "zsh -l"
kitty_graphics = false

[ui]
sidebar_width = 30
sidebar_start_collapsed = true
mouse_capture = false

[ui.toast]
delivery = "system"

[[ui.tab_bar_right]]
type = "hostname"

[[ui.tab_bar_right]]
type = "zoom"

[session]
resume_agents_on_restore = false
startup_per_agent_delay_ms = 250

[keys]
prefix = "ctrl+a"
new_tab = "prefix+t"
split_horizontal = "prefix+minus"
navigate_pane_left = "a"
new_workspace = "prefix+shift+n"
zoom = "alt+z"

[[keys.command]]
key = "prefix+g"
run = "lazygit"
type = "popup"

[[keys.command]]
key = "prefix+s"
command = "git status"
type = "teleport"
`

func TestImportHerdr(t *testing.T) {
	cfg, im, err := config.ImportHerdr(config.Defaults(), []byte(herdrSample), false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Keys.Prefix, []string{"ctrl+a"}) {
		t.Errorf("prefix = %v", cfg.Keys.Prefix)
	}
	want := map[string]map[string][]string{
		"prefix":   {"new_tab": {"t"}, "split_down": {"-"}},
		"navigate": {"focus_left": {"a"}},
		"terminal": {"zoom": {"alt+z"}},
	}
	if !reflect.DeepEqual(cfg.Keys.Modes, want) {
		t.Errorf("modes = %v", cfg.Keys.Modes)
	}
	if len(cfg.Keys.Commands) != 1 || !reflect.DeepEqual(cfg.Keys.Commands[0], config.KeyCommand{Keys: []string{"g"}, Mode: "prefix", Type: "popup", Command: "lazygit"}) {
		t.Errorf("commands = %+v", cfg.Keys.Commands)
	}
	if cfg.Theme.Name != "custom" || cfg.Theme.Custom["base"] != "tokyo-night" || cfg.Theme.Custom["accent"] != "#ff79c6" || cfg.Theme.Custom["fg"] != "#eeeeee" {
		t.Errorf("theme = %q %v", cfg.Theme.Name, cfg.Theme.Custom)
	}
	if cfg.Terminal.Shell != "zsh -l" || cfg.UI.SidebarWidth != 30 || cfg.UI.Sidebar || cfg.UI.Mouse || !cfg.Notify.System {
		t.Errorf("settings = %+v %+v", cfg.Terminal, cfg.UI)
	}
	if cfg.Session.ResumeAgentsOnRestore || cfg.Session.RestoreStagger != 250*time.Millisecond {
		t.Errorf("session = %+v", cfg.Session)
	}
	if len(cfg.UI.StatusRight) != 1 || cfg.UI.StatusRight[0].Type != "hostname" {
		t.Errorf("status = %+v", cfg.UI.StatusRight)
	}
	skipped := strings.Join(im.Skipped, "\n")
	for _, want := range []string{"keys.new_workspace", "keys.command[1]", "ui.tab_bar_right[1]", "theme.custom.surface_dim", "terminal.kitty_graphics", "onboarding"} {
		if !strings.Contains(skipped, want) {
			t.Errorf("%s not reported as skipped:\n%s", want, skipped)
		}
	}
	// What it makes is a valid Hive configuration, which the UI accepts.
	again, warnings, err := config.Parse(config.Render(cfg))
	if err != nil || len(warnings) != 0 || !reflect.DeepEqual(again, cfg) {
		t.Fatalf("round trip: %v %v", err, warnings)
	}
}

func TestImportHerdrDefaultKeys(t *testing.T) {
	cfg, _, err := config.ImportHerdr(config.Defaults(), []byte("[keys]\nzoom = \"prefix+m\"\n"), true)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Keys.Modes["prefix"]
	if !reflect.DeepEqual(p["split_right"], []string{"v"}) || !reflect.DeepEqual(p["zoom"], []string{"m"}) || !reflect.DeepEqual(p["tab_3"], []string{"3"}) || !reflect.DeepEqual(p["focus_next"], []string{"tab"}) {
		t.Fatalf("herdr's keymap with the user's zoom: %v", p)
	}
	if _, _, err := config.ImportHerdr(config.Defaults(), []byte("not toml ["), false); err == nil {
		t.Fatal("bad TOML accepted")
	}
}
