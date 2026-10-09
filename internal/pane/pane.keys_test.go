package pane_test

import (
	"errors"
	"testing"

	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/vt"
)

func TestEncodeKeys(t *testing.T) {
	tests := []struct {
		keys  []string
		modes vt.Modes
		want  string
	}{
		{[]string{"Enter"}, 0, "\r"},
		{[]string{"C-c"}, 0, "\x03"},
		{[]string{"C-["}, 0, "\x1b"},
		{[]string{"M-x"}, 0, "\x1bx"},
		{[]string{"C-M-a"}, 0, "\x1b\x01"},
		{[]string{"Up", "Down"}, 0, "\x1b[A\x1b[B"},
		{[]string{"Up"}, vt.ModeAppCursorKeys, "\x1bOA"},
		{[]string{"q", ":", "w", "q", "Enter"}, 0, "q:wq\r"},
		{[]string{"F5", "PageDown", "BTab"}, 0, "\x1b[15~\x1b[6~\x1b[Z"},
		{[]string{"é"}, 0, "é"},
	}
	for _, tt := range tests {
		got, err := pane.EncodeKeys(tt.keys, tt.modes)
		if err != nil || string(got) != tt.want {
			t.Errorf("EncodeKeys(%q) = %q, %v; want %q", tt.keys, got, err, tt.want)
		}
	}
	for _, bad := range [][]string{{"Hyper"}, {"C-Up"}, {"C-1"}} {
		if _, err := pane.EncodeKeys(bad, 0); !errors.Is(err, pane.ErrInvalid) {
			t.Errorf("EncodeKeys(%q) should fail, got %v", bad, err)
		}
	}
}
