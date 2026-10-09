// Package keymap maps keys to multiplexer actions. Input is modal, like
// tmux: in terminal mode keys go to the focused pane, except a prefix key
// (ctrl+b by default; several may be set), which arms prefix mode for one
// key. Navigate and resize are sticky modes left with esc; copy mode drives
// the scrollback cursor with vim keys.
//
// Every binding can be changed in the [keys] section of the config file:
//
//	[keys]
//	prefix_keys = ["ctrl+b", "ctrl+a"]
//	[keys.prefix]
//	split_right = ["|", "%"]
//	zoom = []            # unbind
//	[keys.terminal]
//	focus_left = "alt+h" # no prefix needed
package keymap

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
)

// Mode is an input mode.
type Mode string

const (
	ModeTerminal Mode = "terminal" // keys go to the focused pane
	ModePrefix   Mode = "prefix"   // one key after the prefix
	ModeNavigate Mode = "navigate" // sticky: move between panes and tabs
	ModeResize   Mode = "resize"   // sticky: move borders
	ModeCopy     Mode = "copy"     // scrollback cursor, selection, search
)

// Modes lists the modes in help order.
var Modes = []Mode{ModeTerminal, ModePrefix, ModeNavigate, ModeResize, ModeCopy}

// Action is something a key does. Config files spell them with
// underscores (split_right); both spellings are accepted.
type Action string

// Workspace actions (any mode but copy).
const (
	SplitRight    Action = "split-right"
	SplitDown     Action = "split-down"
	ClosePane     Action = "close-pane"
	Zoom          Action = "zoom"
	FocusLeft     Action = "focus-left"
	FocusRight    Action = "focus-right"
	FocusUp       Action = "focus-up"
	FocusDown     Action = "focus-down"
	FocusNext     Action = "focus-next"
	FocusPrev     Action = "focus-prev"
	SwapNext      Action = "swap-next"
	SwapPrev      Action = "swap-prev"
	ResizeLeft    Action = "resize-left"
	ResizeRight   Action = "resize-right"
	ResizeUp      Action = "resize-up"
	ResizeDown    Action = "resize-down"
	NewTab        Action = "new-tab"
	CloseTab      Action = "close-tab"
	NextTab       Action = "next-tab"
	PrevTab       Action = "prev-tab"
	NextEnv       Action = "next-env"
	PrevEnv       Action = "prev-env"
	RenameTab     Action = "rename-tab"
	RenamePane    Action = "rename-pane"
	Popup         Action = "popup"
	Goto          Action = "goto"
	Help          Action = "help"
	Overview      Action = "overview"
	ToggleSidebar Action = "toggle-sidebar"
	EditScroll    Action = "edit-scrollback"
	Paste         Action = "paste"
	Detach        Action = "detach"
	SendPrefix    Action = "send-prefix"
	EnterNavigate Action = "mode-navigate"
	EnterResize   Action = "mode-resize"
	EnterCopy     Action = "mode-copy"
	ExitMode      Action = "mode-terminal"
)

// Tab1 … Tab9 select a tab by its number.
func TabAction(n int) Action { return Action(fmt.Sprintf("tab-%d", n)) }

// TabNumber returns n for TabAction(n).
func (a Action) TabNumber() (int, bool) {
	var n int
	if _, err := fmt.Sscanf(string(a), "tab-%d", &n); err != nil || n < 1 || n > 9 {
		return 0, false
	}
	return n, true
}

// Copy-mode actions.
const (
	CopyLeft          Action = "copy-left"
	CopyRight         Action = "copy-right"
	CopyUp            Action = "copy-up"
	CopyDown          Action = "copy-down"
	CopyWordNext      Action = "copy-word-next"
	CopyWordPrev      Action = "copy-word-prev"
	CopyWordEnd       Action = "copy-word-end"
	CopyLineStart     Action = "copy-line-start"
	CopyLineEnd       Action = "copy-line-end"
	CopyFirstNonBlank Action = "copy-first-nonblank"
	CopyTop           Action = "copy-top"
	CopyBottom        Action = "copy-bottom"
	CopyScreenTop     Action = "copy-screen-top"
	CopyScreenMiddle  Action = "copy-screen-middle"
	CopyScreenBottom  Action = "copy-screen-bottom"
	CopyHalfUp        Action = "copy-half-up"
	CopyHalfDown      Action = "copy-half-down"
	CopyPageUp        Action = "copy-page-up"
	CopyPageDown      Action = "copy-page-down"
	CopySearchForward Action = "copy-search-forward"
	CopySearchBack    Action = "copy-search-backward"
	CopySearchNext    Action = "copy-search-next"
	CopySearchPrev    Action = "copy-search-prev"
	CopySelect        Action = "copy-select"
	CopySelectLine    Action = "copy-select-line"
	CopyYank          Action = "copy-yank"
	CopyExit          Action = "copy-exit"
)

// descriptions are shown in the help overlay; they also define which
// actions exist.
var descriptions = map[Action]string{
	SplitRight: "split the pane, new pane on the right", SplitDown: "split the pane, new pane below",
	ClosePane: "close the pane (asks first)", Zoom: "zoom the pane to fill the tab (toggle)",
	FocusLeft: "focus the pane to the left", FocusRight: "focus the pane to the right",
	FocusUp: "focus the pane above", FocusDown: "focus the pane below",
	FocusNext: "focus the next pane", FocusPrev: "focus the previous pane",
	SwapNext: "swap the pane with the next one", SwapPrev: "swap the pane with the previous one",
	ResizeLeft: "move the left border", ResizeRight: "move the right border",
	ResizeUp: "move the top border", ResizeDown: "move the bottom border",
	NewTab: "new tab", CloseTab: "close the tab (asks first)",
	NextTab: "next tab", PrevTab: "previous tab",
	NextEnv: "next environment", PrevEnv: "previous environment",
	RenameTab: "rename the tab", RenamePane: "rename the pane",
	Popup: "open a shell in a popup", Goto: "go to an agent or pane (fuzzy)",
	Help: "show key bindings", Overview: "Overview dashboard (toggle)",
	ToggleSidebar: "show or hide the sidebar", EditScroll: "open the pane's scrollback in $EDITOR",
	Paste: "paste the last copied text", Detach: "leave the UI (agents keep running)",
	SendPrefix:    "send the prefix key to the pane",
	EnterNavigate: "navigate mode (sticky)", EnterResize: "resize mode (sticky)",
	EnterCopy: "copy mode", ExitMode: "back to terminal mode",

	CopyLeft: "left", CopyRight: "right", CopyUp: "up", CopyDown: "down",
	CopyWordNext: "next word", CopyWordPrev: "previous word", CopyWordEnd: "end of word",
	CopyLineStart: "start of line", CopyLineEnd: "end of line", CopyFirstNonBlank: "first non-blank",
	CopyTop: "top of history", CopyBottom: "bottom",
	CopyScreenTop: "top of screen", CopyScreenMiddle: "middle of screen", CopyScreenBottom: "bottom of screen",
	CopyHalfUp: "half a page up", CopyHalfDown: "half a page down",
	CopyPageUp: "page up", CopyPageDown: "page down",
	CopySearchForward: "search down", CopySearchBack: "search up",
	CopySearchNext: "next match", CopySearchPrev: "previous match",
	CopySelect: "select (toggle)", CopySelectLine: "select lines (toggle)",
	CopyYank: "copy the selection and leave", CopyExit: "leave copy mode",
}

func init() {
	for n := 1; n <= 9; n++ {
		descriptions[TabAction(n)] = fmt.Sprintf("tab %d", n)
	}
}

// Describe returns an action's help text.
func Describe(a Action) string { return descriptions[a] }

// IsCopy reports whether a belongs to copy mode.
func (a Action) IsCopy() bool { return strings.HasPrefix(string(a), "copy-") }

// ParseAction accepts split_right or split-right.
func ParseAction(s string) (Action, error) {
	a := Action(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "_", "-"))
	if _, ok := descriptions[a]; !ok {
		return "", fmt.Errorf("unknown action %q", s)
	}
	return a, nil
}

// DefaultPrefix is the prefix key unless configured.
const DefaultPrefix = "ctrl+b"

// defaults lists every built-in binding.
func defaults() map[Mode]map[Action][]string {
	focus := map[Action][]string{
		FocusLeft: {"h", "left"}, FocusRight: {"l", "right"}, FocusUp: {"k", "up"}, FocusDown: {"j", "down"},
	}
	prefix := map[Action][]string{
		SplitRight: {"%", "|"}, SplitDown: {`"`, "-"},
		ClosePane: {"x"}, Zoom: {"z"},
		FocusNext: {"o"}, FocusPrev: {";"},
		SwapNext: {"}"}, SwapPrev: {"{"},
		ResizeLeft: {"H", "alt+left"}, ResizeRight: {"L", "alt+right"},
		ResizeUp: {"K", "alt+up"}, ResizeDown: {"J", "alt+down"},
		NewTab: {"c"}, CloseTab: {"&"}, NextTab: {"n"}, PrevTab: {"p"},
		NextEnv: {")"}, PrevEnv: {"("},
		RenameTab: {","}, RenamePane: {"."},
		Popup: {"f"}, Goto: {"w", "s"}, Help: {"?"}, Overview: {"O"},
		ToggleSidebar: {"b"}, EditScroll: {"e"}, Paste: {"]"}, Detach: {"d"},
		EnterNavigate: {"space"}, EnterResize: {"r"}, EnterCopy: {"[", "v"},
		ExitMode: {"esc"},
	}
	maps.Copy(prefix, focus)
	for n := 1; n <= 9; n++ {
		prefix[TabAction(n)] = []string{fmt.Sprint(n)}
	}
	navigate := map[Action][]string{
		NextTab: {"n", "tab"}, PrevTab: {"p", "shift+tab"}, NextEnv: {")"}, PrevEnv: {"("},
		Zoom: {"z"}, ClosePane: {"x"}, SplitRight: {"%", "|"}, SplitDown: {`"`, "-"},
		SwapNext: {"}"}, SwapPrev: {"{"}, FocusNext: {"o"}, NewTab: {"c"},
		Goto: {"w", "/"}, EnterResize: {"r"}, EnterCopy: {"["},
		ExitMode: {"esc", "q", "enter", "i"},
	}
	maps.Copy(navigate, focus)
	for n := 1; n <= 9; n++ {
		navigate[TabAction(n)] = []string{fmt.Sprint(n)}
	}
	resize := map[Action][]string{
		ResizeLeft: {"h", "left"}, ResizeRight: {"l", "right"}, ResizeUp: {"k", "up"}, ResizeDown: {"j", "down"},
		Zoom: {"z"}, EnterNavigate: {"space"}, ExitMode: {"esc", "q", "enter", "i"},
	}
	copyMode := map[Action][]string{
		CopyLeft: {"h", "left"}, CopyRight: {"l", "right"}, CopyUp: {"k", "up"}, CopyDown: {"j", "down"},
		CopyWordNext: {"w"}, CopyWordPrev: {"b"}, CopyWordEnd: {"e"},
		CopyLineStart: {"0", "home"}, CopyLineEnd: {"$", "end"}, CopyFirstNonBlank: {"^"},
		CopyTop: {"g"}, CopyBottom: {"G"},
		CopyScreenTop: {"H"}, CopyScreenMiddle: {"M"}, CopyScreenBottom: {"L"},
		CopyHalfUp: {"ctrl+u"}, CopyHalfDown: {"ctrl+d"},
		CopyPageUp: {"ctrl+b", "pgup"}, CopyPageDown: {"ctrl+f", "pgdown"},
		CopySearchForward: {"/"}, CopySearchBack: {"?"},
		CopySearchNext: {"n"}, CopySearchPrev: {"N"},
		CopySelect: {"v", "space"}, CopySelectLine: {"V"},
		CopyYank: {"y", "enter"}, CopyExit: {"q", "esc"},
	}
	return map[Mode]map[Action][]string{
		ModeTerminal: {},
		ModePrefix:   prefix,
		ModeNavigate: navigate,
		ModeResize:   resize,
		ModeCopy:     copyMode,
	}
}

// Overrides are the [keys] section of the config file: the prefix keys, and
// per mode, actions whose keys replace the built-in ones (an empty list
// unbinds the action).
type Overrides struct {
	Prefix []string
	Modes  map[Mode]map[Action][]string
}

// Keymap resolves keys in each mode.
type Keymap struct {
	prefixes []string
	keys     map[Mode]map[string]Action // mode → normalized key → action
	actions  map[Mode]map[Action][]string
}

// Default returns the built-in key map.
func Default() *Keymap {
	km, _ := New(Overrides{})
	return km
}

// New builds a key map from the built-in bindings and o. Problems (unknown
// keys, actions in the wrong mode, two actions on one key) are returned as
// warnings; the offending entry is skipped and everything else applies.
func New(o Overrides) (*Keymap, []string) {
	var warnings []string
	actions := defaults()
	for _, m := range slices.Sorted(maps.Keys(o.Modes)) {
		if _, ok := actions[m]; !ok {
			warnings = append(warnings, fmt.Sprintf("keys.%s: unknown mode (want one of %s)", m, modeList()))
			continue
		}
		for _, a := range slices.Sorted(maps.Keys(o.Modes[m])) {
			if _, ok := descriptions[a]; !ok {
				warnings = append(warnings, fmt.Sprintf("keys.%s: unknown action %q", m, a))
				continue
			}
			if a.IsCopy() != (m == ModeCopy) {
				warnings = append(warnings, fmt.Sprintf("keys.%s.%s: action not available in %s mode", m, underscore(a), m))
				continue
			}
			actions[m][a] = o.Modes[m][a]
		}
	}

	km := &Keymap{keys: map[Mode]map[string]Action{}, actions: map[Mode]map[Action][]string{}}
	prefixes := o.Prefix
	if prefixes == nil {
		prefixes = []string{DefaultPrefix}
	}
	for _, p := range prefixes {
		k, err := Normalize(p)
		if err != nil {
			warnings = append(warnings, "keys.prefix: "+err.Error())
			continue
		}
		if !slices.Contains(km.prefixes, k) {
			km.prefixes = append(km.prefixes, k)
		}
	}
	if len(km.prefixes) == 0 {
		warnings = append(warnings, "keys.prefix: no usable prefix key; using "+DefaultPrefix)
		km.prefixes = []string{DefaultPrefix}
	}

	for _, m := range Modes {
		km.keys[m] = map[string]Action{}
		km.actions[m] = map[Action][]string{}
		// The user's bindings go first: a built-in binding on a key the user
		// gave to another action quietly gives it up.
		user := o.Modes[m]
		order := slices.SortedFunc(maps.Keys(actions[m]), func(a, b Action) int {
			_, ua := user[a]
			_, ub := user[b]
			if ua != ub {
				if ua {
					return -1
				}
				return 1
			}
			return strings.Compare(string(a), string(b))
		})
		for _, a := range order {
			_, mine := user[a]
			for _, raw := range actions[m][a] {
				k, err := Normalize(raw)
				if err != nil {
					warnings = append(warnings, fmt.Sprintf("keys.%s.%s: %v", m, underscore(a), err))
					continue
				}
				if m == ModeTerminal && slices.Contains(km.prefixes, k) {
					warnings = append(warnings, fmt.Sprintf("keys.terminal.%s: %q is a prefix key", underscore(a), k))
					continue
				}
				if prev, dup := km.keys[m][k]; dup && prev != a {
					if _, prevMine := user[prev]; prevMine && !mine {
						continue // the user moved this key elsewhere
					}
					warnings = append(warnings, fmt.Sprintf("keys.%s: %q is bound to both %s and %s; keeping %s", m, k, underscore(prev), underscore(a), underscore(prev)))
					continue
				}
				km.keys[m][k] = a
				km.actions[m][a] = append(km.actions[m][a], k)
			}
		}
	}
	// In prefix mode, a prefix key pressed again sends it to the pane.
	for _, p := range km.prefixes {
		if _, taken := km.keys[ModePrefix][p]; !taken {
			km.keys[ModePrefix][p] = SendPrefix
			km.actions[ModePrefix][SendPrefix] = append(km.actions[ModePrefix][SendPrefix], p)
		}
	}
	return km, warnings
}

func modeList() string {
	var s []string
	for _, m := range Modes {
		s = append(s, string(m))
	}
	return strings.Join(s, ", ")
}

func underscore(a Action) string { return strings.ReplaceAll(string(a), "-", "_") }

// Prefixes returns the prefix keys.
func (km *Keymap) Prefixes() []string { return slices.Clone(km.prefixes) }

// IsPrefix reports whether k is a prefix key, returning its name.
func (km *Keymap) IsPrefix(k uv.Key) (string, bool) {
	for _, n := range Names(k) {
		if slices.Contains(km.prefixes, n) {
			return n, true
		}
	}
	return "", false
}

// Lookup returns the action k is bound to in mode m.
func (km *Keymap) Lookup(m Mode, k uv.Key) (Action, bool) {
	for _, n := range Names(k) {
		if a, ok := km.keys[m][n]; ok {
			return a, true
		}
	}
	return "", false
}

// LookupName is Lookup for a normalized key string.
func (km *Keymap) LookupName(m Mode, key string) (Action, bool) {
	a, ok := km.keys[m][key]
	return a, ok
}

// Keys returns the keys bound to a in mode m.
func (km *Keymap) Keys(m Mode, a Action) []string { return slices.Clone(km.actions[m][a]) }

// Entry is one line of the help overlay.
type Entry struct {
	Mode        Mode
	Action      Action
	Keys        []string
	Description string
}

// Entries lists every binding, by mode then action, for the help overlay.
func (km *Keymap) Entries() []Entry {
	var out []Entry
	for _, m := range Modes {
		for _, a := range slices.Sorted(maps.Keys(km.actions[m])) {
			out = append(out, Entry{Mode: m, Action: a, Keys: slices.Clone(km.actions[m][a]), Description: descriptions[a]})
		}
	}
	return out
}

// Filter returns the entries whose mode, keys, action or description
// contain every word of query (case-insensitive).
func Filter(entries []Entry, query string) []Entry {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return entries
	}
	var out []Entry
	for _, e := range entries {
		hay := strings.ToLower(fmt.Sprintf("%s %s %s %s", e.Mode, strings.Join(e.Keys, " "), e.Action, e.Description))
		match := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				match = false
				break
			}
		}
		if match {
			out = append(out, e)
		}
	}
	return out
}
