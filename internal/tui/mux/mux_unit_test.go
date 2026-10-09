package mux

import (
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/git"
	"github.com/admirable-oss/hive/internal/vt"
)

func TestPasteBytes(t *testing.T) {
	if got := string(pasteBytes("a\nb\r\nc", 0)); got != "a\rb\rc" {
		t.Errorf("plain paste = %q: newlines are typed as Enter", got)
	}
	got := string(pasteBytes("x\x1b[201~y\n", vt.ModeBracketedPaste))
	if got != "\x1b[200~xy\n\x1b[201~" {
		t.Errorf("bracketed paste = %q: wrapped, and cannot end the bracket early", got)
	}
}

func cells(s string) []vt.Cell {
	var out []vt.Cell
	for _, r := range s {
		out = append(out, vt.Cell{Content: string(r), Width: 1})
	}
	return out
}

func TestURLInLine(t *testing.T) {
	line := cells("see https://example.com/a_(b)?q=1. and (http://x.io/y) or file:///tmp/f")
	tests := []struct {
		x    int
		want string
	}{
		{0, ""},
		{4, "https://example.com/a_(b)?q=1"},
		{30, "https://example.com/a_(b)?q=1"},
		{33, ""}, // the full stop after the link
		{40, "http://x.io/y"},
		{len("see https://example.com/a_(b)?q=1. and (http://x.io/y) or file:///tm"), "file:///tmp/f"},
	}
	for _, tt := range tests {
		if got := urlInLine(line, tt.x); got != tt.want {
			t.Errorf("url at %d = %q, want %q", tt.x, got, tt.want)
		}
	}
	// Columns, not bytes: a wide character before the link shifts it.
	wide := append([]vt.Cell{{Content: "日", Width: 2}, {Width: 0}}, cells(" https://a.b")...)
	if got := urlInLine(wide, 3); got != "https://a.b" {
		t.Errorf("after a wide character: %q", got)
	}
}

func TestEnvName(t *testing.T) {
	for _, tt := range []struct {
		base  string
		taken []string
		want  string
	}{
		{"api", nil, "api"},
		{"my project", nil, "my-project"},
		{"api", []string{"api", "api-2"}, "api-3"},
		{"...", nil, "workspace"},
	} {
		if got := envName(tt.base, tt.taken); got != tt.want {
			t.Errorf("envName(%q, %v) = %q, want %q", tt.base, tt.taken, got, tt.want)
		}
	}
}

func TestEnvForDir(t *testing.T) {
	envs := []environment.Environment{{ID: "repo", Path: "/src/repo"}, {ID: "sub", Path: "/src/repo/sub"}, {ID: "other", Path: "/src/repository"}}
	for dir, want := range map[string]string{
		"/src/repo": "repo", "/src/repo/x": "repo", "/src/repo/sub/y": "sub", "/src/repository": "other", "/elsewhere": "", "": "",
	} {
		if got := envForDir(envs, dir); got != want {
			t.Errorf("envForDir(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestGitSummary(t *testing.T) {
	for _, tt := range []struct {
		s    *git.Status
		want string
	}{
		{nil, ""},
		{&git.Status{Branch: "main"}, "main"},
		{&git.Status{Branch: "main", Ahead: 1, Behind: 2, Modified: 1}, "main ↑1 ↓2 *"},
		{&git.Status{Detached: true, Head: "abc123", Conflicts: 1}, "@abc123 !"},
	} {
		if got := gitSummary(tt.s); got != tt.want {
			t.Errorf("gitSummary(%+v) = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestSanitizeLog(t *testing.T) {
	in := "\x1b[1;31mred\x1b[0m line\r\n\x1b]0;title\x07next\x1b]8;;http://x\x1b\\link\x07\tend"
	if got := sanitizeLog(in); got != "red line\nnextlink\tend" {
		t.Errorf("sanitizeLog = %q", got)
	}
}

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		5 * time.Second: "5s", 3 * time.Minute: "3m", 125 * time.Minute: "2h5m", 50 * time.Hour: "2d",
	} {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestInputEditing(t *testing.T) {
	in := newInput("hello world")
	in.edit(key("ctrl+w"))
	if in.String() != "hello " {
		t.Fatalf("ctrl+w: %q", in.String())
	}
	in.edit(key("home"))
	in.edit(key("X"))
	in.edit(key("right"))
	in.edit(key("backspace"))
	if in.String() != "Xello " || in.cursor != 1 {
		t.Fatalf("edit: %q at %d", in.String(), in.cursor)
	}
	if in.edit(key("ctrl+t")) {
		t.Fatal("an unknown control key is not text")
	}
	in.insert("a\nb\x07")
	if in.String() != "Xa bello " {
		t.Fatalf("insert sanitises: %q", in.String())
	}
}

func TestHighlightMatches(t *testing.T) {
	st := uv.Style{Attrs: uv.AttrBold}
	got := highlightMatches("abcd", []int{1, 2}, st)
	if len(got) != 3 || got[0].Text != "a" || got[1].Text != "bc" || got[1].Style != st || got[2].Text != "d" {
		t.Fatalf("spans = %+v", got)
	}
}

func TestFramePacing(t *testing.T) {
	now := time.Unix(1000, 0)
	a := &App{dirty: true, sized: true, lastInput: now.Add(-time.Second)}
	if draw, wait := a.frameDue(now, now.Add(-2*time.Millisecond)); draw || wait != 6*time.Millisecond {
		t.Errorf("output right after a frame waits for the cap: draw=%v wait=%v", draw, wait)
	}
	if draw, _ := a.frameDue(now, now.Add(-frameInterval)); !draw {
		t.Error("output after the cap is drawn")
	}
	a.lastInput = now.Add(-10 * time.Millisecond)
	if draw, _ := a.frameDue(now, now.Add(-time.Millisecond)); !draw {
		t.Error("the echo of a key is drawn at once")
	}
	a.urgent, a.lastInput = true, time.Time{}
	if draw, _ := a.frameDue(now, now); !draw {
		t.Error("input is drawn at once")
	}
	a.dirty = false
	if draw, wait := a.frameDue(now, now); draw || wait != 0 {
		t.Error("nothing changed: nothing to draw")
	}
}
