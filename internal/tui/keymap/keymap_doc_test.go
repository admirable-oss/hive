package keymap_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/tui/keymap"
)

var update = flag.Bool("update", false, "rewrite docs/keybindings.md")

// docPath is the generated key reference.
var docPath = filepath.Join("..", "..", "..", "docs", "keybindings.md")

// TestKeybindingsDocIsCurrent keeps docs/keybindings.md in step with the
// built-in bindings; go test -update rewrites it.
func TestKeybindingsDocIsCurrent(t *testing.T) {
	got := keybindingsDoc(keymap.Default())
	if *update {
		if err := os.WriteFile(docPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("%v (go test -update writes it)", err)
	}
	if string(want) != got {
		t.Errorf("%s is out of date with the key map; run go test ./internal/tui/keymap -update", docPath)
	}
}

var modeIntro = map[keymap.Mode]string{
	keymap.ModeTerminal: "Keys go to the focused pane. Bindings here work without the prefix; there are none by default.",
	keymap.ModePrefix:   "One key after the prefix, then back to terminal mode.",
	keymap.ModeNavigate: "Sticky: keys move between panes and tabs until `esc`.",
	keymap.ModeResize:   "Sticky: keys move the focused pane's borders until `esc`.",
	keymap.ModeCopy:     "Vim keys over the pane's history and screen. Search is case-insensitive unless the query has a capital letter.",
}

func keybindingsDoc(km *keymap.Keymap) string {
	var b strings.Builder
	prefix := km.Prefixes()[0]
	b.WriteString(`# Key bindings

<!-- Generated from internal/tui/keymap: go test ./internal/tui/keymap -update. Do not edit by hand. -->

Hive's UI is modal, like tmux. In **terminal mode** keys go to the focused
pane. The **prefix** key (` + "`" + prefix + "`" + ` unless configured) arms **prefix mode**
for one key: ` + "`" + prefix + "` then `%`" + ` splits the pane, for example. Press the prefix
twice to send it to the pane. ` + "`" + prefix + " ?`" + ` lists every binding in the UI, with a
filter.

Every binding can be changed in the configuration file (` + "`hive config path`" + `);
problems are reported as warnings and the default is kept.
` + "`hive config reset-keys`" + ` puts the defaults back.

` + "```toml" + `
[keys]
prefix_keys = ["ctrl+b", "ctrl+a"]   # several prefixes are allowed

[keys.prefix]
split_right = ["|", "%"]             # replace an action's keys
zoom = []                            # unbind it

[keys.terminal]
focus_left = "alt+h"                 # works without the prefix
` + "```" + `

Actions are written with underscores in the file (` + "`split_right`" + `) and keys as
` + "`ctrl+x`, `alt+x`, `shift+tab`, `C-x`, `M-x`" + `, a character (` + "`%`, `A`" + `) or a name
(` + "`enter`, `esc`, `space`, `tab`, `up`, `pgdown`, `f5`" + `).

## Mouse

- Click a pane to focus it, a tab to show it, ` + "`+`" + ` for a new tab, a sidebar row to
  switch environment or go to an agent. A tab marked • has output you have
  not seen.
- Drag a border between panes, or the sidebar's edge, to resize.
- Drag over text to copy it; double-click copies a word. Ctrl-click opens a
  link: an OSC 8 hyperlink, or a URL in the text. Hyperlinks also reach your
  terminal, so its own link handling (cmd-click, say) works too.
- The wheel scrolls into the pane's history (copy mode); scrolling back to the
  bottom leaves it.
- Programs that use the mouse themselves (vim, htop) get it; hold shift to
  select with the outer terminal instead.
`)
	var mode keymap.Mode
	for _, e := range km.Entries() {
		if e.Mode != mode {
			mode = e.Mode
			title := strings.ToUpper(string(mode[:1])) + string(mode[1:]) + " mode"
			if mode == keymap.ModePrefix {
				title += " (after `" + prefix + "`)"
			}
			fmt.Fprintf(&b, "\n## %s\n\n%s\n\n| Keys | Action | |\n|---|---|---|\n", title, modeIntro[mode])
		}
		keys := make([]string, len(e.Keys))
		for i, k := range e.Keys {
			keys[i] = "`" + strings.ReplaceAll(k, "|", `\|`) + "`"
		}
		action := strings.ReplaceAll(string(e.Action), "-", "_")
		fmt.Fprintf(&b, "| %s | `%s` | %s |\n", strings.Join(keys, " "), action, e.Description)
	}
	return b.String()
}
