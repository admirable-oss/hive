package hiveapi

import (
	"path/filepath"
	"testing"
)

func TestSocketPath(t *testing.T) {
	t.Setenv("HIVE_SOCKET_PATH", "/run/pane.sock")
	t.Setenv("HIVE_HOME", "/h")
	t.Setenv("HIVE_SESSION", "")
	for _, tc := range []struct {
		opts Options
		want string
	}{
		{Options{}, "/run/pane.sock"},                            // inside a pane
		{Options{Socket: "/x.sock"}, "/x.sock"},                  // explicit
		{Options{Home: "/other"}, "/other/hive.sock"},            // another home: not the pane's
		{Options{Session: "work"}, "/h/sessions/work/hive.sock"}, // another session
	} {
		got, err := tc.opts.SocketPath()
		if err != nil || filepath.Clean(got) != tc.want {
			t.Errorf("%+v: %q %v, want %q", tc.opts, got, err, tc.want)
		}
	}
	t.Setenv("HIVE_SOCKET_PATH", "")
	if got, _ := (Options{}).SocketPath(); got != "/h/hive.sock" {
		t.Errorf("outside a pane: %q", got)
	}
}
