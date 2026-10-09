package keymap

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
)

// modifierOrder is how ultraviolet writes modifiers in a keystroke, so a
// normalized key string compares equal to Key.Keystroke.
var modifierOrder = []string{"ctrl", "alt", "shift", "meta", "hyper", "super"}

// aliases maps other common spellings to ultraviolet's key names.
var aliases = map[string]string{
	"escape": "esc", "return": "enter", "ret": "enter", "cr": "enter",
	"pageup": "pgup", "pagedown": "pgdown", "page_up": "pgup", "page_down": "pgdown",
	"del": "delete", "bs": "backspace", "ins": "insert",
	"arrowup": "up", "arrowdown": "down", "arrowleft": "left", "arrowright": "right",
	"plus": "+", "minus": "-",
}

// named are the multi-letter key names ultraviolet produces.
var named = map[string]bool{
	"enter": true, "tab": true, "backspace": true, "esc": true, "space": true,
	"up": true, "down": true, "left": true, "right": true, "begin": true,
	"find": true, "insert": true, "delete": true, "select": true,
	"pgup": true, "pgdown": true, "home": true, "end": true,
}

func init() {
	for i := 1; i <= 20; i++ {
		named[fmt.Sprintf("f%d", i)] = true
	}
}

// Normalize returns the canonical form of a key written in a config file or
// a binding table: lower-case modifiers in ultraviolet's order, key names
// unaliased, and shift+letter written as the upper-case letter (which is
// what a terminal reports). "Ctrl+B", "C-b" and "ctrl+b" are all "ctrl+b".
func Normalize(s string) (string, error) {
	if s == " " {
		return "space", nil
	}
	raw := strings.TrimSpace(s)
	if raw == "" {
		return "", fmt.Errorf("empty key")
	}
	if raw == "+" {
		return "+", nil
	}
	// tmux/emacs style: C-b, M-x, S-tab.
	if len(raw) > 2 && raw[1] == '-' && strings.ContainsRune("CMS", rune(raw[0])) {
		prefix := map[byte]string{'C': "ctrl+", 'M': "alt+", 'S': "shift+"}[raw[0]]
		raw = prefix + raw[2:]
	}
	parts := strings.Split(raw, "+")
	// "ctrl++" splits into ["ctrl", "", ""]: the key is "+".
	if strings.HasSuffix(raw, "++") {
		parts = append(parts[:len(parts)-2], "+")
	}
	key := parts[len(parts)-1]
	mods := map[string]bool{}
	for _, m := range parts[:len(parts)-1] {
		m = strings.ToLower(strings.TrimSpace(m))
		switch m {
		case "control":
			m = "ctrl"
		case "option", "opt", "meta":
			// Terminals report Option/Meta as alt.
			m = "alt"
		case "cmd", "command", "win":
			m = "super"
		}
		known := false
		for _, o := range modifierOrder {
			if o == m {
				known = true
			}
		}
		if !known {
			return "", fmt.Errorf("key %q: unknown modifier %q", s, m)
		}
		mods[m] = true
	}
	if key == "" {
		return "", fmt.Errorf("key %q: missing key after modifiers", s)
	}
	if utf8.RuneCountInString(key) > 1 {
		lk := strings.ToLower(key)
		if a, ok := aliases[lk]; ok {
			lk = a
		}
		if !named[lk] && utf8.RuneCountInString(lk) > 1 {
			return "", fmt.Errorf("key %q: unknown key name %q", s, key)
		}
		key = lk
	}
	// With ctrl or alt, letters are reported lower-case plus shift.
	r, _ := utf8.DecodeRuneInString(key)
	if utf8.RuneCountInString(key) == 1 && unicode.IsUpper(r) && (mods["ctrl"] || mods["alt"]) {
		key = string(unicode.ToLower(r))
		mods["shift"] = true
	}
	// Plain shift+letter is the upper-case letter.
	if utf8.RuneCountInString(key) == 1 && mods["shift"] && !mods["ctrl"] && !mods["alt"] && !mods["meta"] && !mods["super"] && !mods["hyper"] && unicode.IsLetter(r) {
		delete(mods, "shift")
		key = string(unicode.ToUpper(r))
	}
	var b strings.Builder
	for _, m := range modifierOrder {
		if mods[m] {
			b.WriteString(m + "+")
		}
	}
	b.WriteString(key)
	return b.String(), nil
}

// MustNormalize is Normalize for built-in tables, where an error is a bug.
func MustNormalize(s string) string {
	k, err := Normalize(s)
	if err != nil {
		panic(err)
	}
	return k
}

// Names returns the strings a key event may match a binding by, most
// specific first: the text it types ("?", "A", "%"), then its keystroke
// ("ctrl+b", "alt+left", "shift+tab").
func Names(k uv.Key) []string {
	stroke := k.Keystroke()
	if s := k.String(); s != stroke {
		return []string{s, stroke}
	}
	return []string{stroke}
}

// Display renders a key for the help overlay and status hints.
func Display(k string) string {
	switch k {
	case "space":
		return "␣"
	case "enter":
		return "⏎"
	}
	return strings.ReplaceAll(k, "ctrl+", "C-")
}
