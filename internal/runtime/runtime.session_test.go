package runtime

import "testing"

func TestShellJoin(t *testing.T) {
	for _, tt := range []struct {
		argv []string
		want string
	}{
		{[]string{"claude", "--resume", "1f2e-ab"}, "claude --resume 1f2e-ab"},
		{[]string{"/opt/my agent/bin/codex", "resume", "it's"}, `'/opt/my agent/bin/codex' resume 'it'\''s'`},
		{[]string{"node", "/usr/lib/cli.js", ""}, "node /usr/lib/cli.js ''"},
		{[]string{"x", "$(rm -rf ~)"}, "x '$(rm -rf ~)'"},
	} {
		if got := shellJoin(tt.argv); got != tt.want {
			t.Errorf("shellJoin(%q) = %s, want %s", tt.argv, got, tt.want)
		}
	}
}

func TestWithoutVars(t *testing.T) {
	got := withoutVars(map[string]string{"HIVE_PANE_ID": "p1", "HIVE_TAB_ID": "t1", "A": "1"}, paneVars...)
	if len(got) != 1 || got["A"] != "1" {
		t.Fatalf("withoutVars = %v", got)
	}
	if withoutVars(map[string]string{"HIVE_PANE_ID": "p1"}, paneVars...) != nil {
		t.Fatal("nothing left is nil")
	}
}
