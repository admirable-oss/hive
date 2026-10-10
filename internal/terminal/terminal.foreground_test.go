package terminal_test

import (
	"context"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/terminal"
)

func TestPTYForeground(t *testing.T) {
	sess, err := terminal.PTYFactory{}.Open(context.Background(), terminal.Command{
		Path: "/bin/sh", Args: []string{"-c", "sleep 0.2; exec sleep 30"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	fr, ok := sess.(terminal.ForegroundReader)
	if !ok {
		t.Fatal("PTY sessions report their foreground")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		fg, err := fr.Foreground(context.Background())
		if err == nil && len(fg.Args) == 2 && fg.Args[0] == "sleep" && fg.Args[1] == "30" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("foreground = %+v, %v; want sleep 30", fg, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
