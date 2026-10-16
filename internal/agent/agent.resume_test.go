package agent

import (
	"slices"
	"strings"
	"testing"
)

func TestResumeCommand(t *testing.T) {
	ms, _, err := LoadManifests("")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		kind    string
		argv    []string
		session string
		want    []string
	}{
		{"claude", []string{"claude", "--model", "opus"}, "1f2e", []string{"claude", "--resume", "1f2e"}},
		{"claude", []string{"/opt/bin/claude"}, "", []string{"/opt/bin/claude", "--continue"}},
		{"claude", []string{"node", "/usr/lib/claude/cli.js", "-p"}, "ab", []string{"node", "/usr/lib/claude/cli.js", "--resume", "ab"}},
		{"codex", []string{"codex", "--yolo"}, "019a", []string{"codex", "resume", "019a"}},
		{"codex", nil, "", []string{"codex", "resume", "--last"}},
		{"aider", []string{"aider"}, "ignored", []string{"aider", "--restore-chat-history"}},
	}
	for _, tt := range tests {
		got, ok := byID(ms, tt.kind).ResumeCommand(tt.argv, tt.session)
		if !ok || !slices.Equal(got, tt.want) {
			t.Errorf("%s %v %q: %v %v, want %v", tt.kind, tt.argv, tt.session, got, ok, tt.want)
		}
	}
	if _, ok := (&Manifest{Start: Start{Command: []string{"x"}}}).ResumeCommand([]string{"x"}, "id"); ok {
		t.Error("a manifest without [resume] cannot resume")
	}
}

func TestResumeArgsNeedTheSessionID(t *testing.T) {
	_, err := ParseManifest([]byte("id = \"x\"\nname = \"X\"\n[detect]\nprocess = [\"x\"]\n[resume]\nargs = [\"--resume\"]\n"), "x.toml")
	if err == nil || !strings.Contains(err.Error(), "{session_id}") {
		t.Fatalf("err = %v", err)
	}
}

func TestInputImageIsChecked(t *testing.T) {
	const head = "id = \"x\"\nname = \"X\"\n[detect]\nprocess = [\"x\"]\n[input]\n"
	if _, err := ParseManifest([]byte(head+"image = \"clipboard\"\n"), "x.toml"); err == nil || !strings.Contains(err.Error(), "input.image") {
		t.Fatalf("err = %v", err)
	}
	m, err := ParseManifest([]byte(head+"image = \"path\"\n"), "x.toml")
	if err != nil || m.Input.Image != "path" {
		t.Fatalf("m = %+v, err = %v", m, err)
	}
	ms, _, err := LoadManifests("")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"claude", "codex"} {
		if m := byID(ms, id); m == nil || m.Input.Image != "path" {
			t.Errorf("built-in %s manifest does not declare image = \"path\"", id)
		}
	}
}

// TestWindowsProgramNames: an agent is recognised by its program name
// whatever the path separator, extension or case Windows gives it.
func TestWindowsProgramNames(t *testing.T) {
	m := &Manifest{ID: "claude", Name: "Claude", Detect: Detect{Process: []string{"claude"}}}
	for _, argv := range [][]string{
		{`C:\Users\me\AppData\Roaming\npm\claude.cmd`},
		{`C:\Program Files\Claude\CLAUDE.EXE`, "--resume"},
		{`C:\Program Files\nodejs\node.exe`, `C:\Users\me\bin\claude.js`},
		{"claude.ps1"},
	} {
		if !m.Matches(argv) {
			t.Errorf("%q not recognised", argv)
		}
	}
	if m.Matches([]string{`C:\tools\claudette.exe`}) {
		t.Error("claudette recognised as claude")
	}
}
