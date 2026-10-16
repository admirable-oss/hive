//go:build windows

package terminal_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/terminal"
	"github.com/admirable-oss/hive/internal/vt"
)

func openConPTY(t *testing.T, path string, args ...string) terminal.Session {
	t.Helper()
	sess, err := terminal.PTYFactory{StopGrace: 500 * time.Millisecond}.Open(context.Background(), terminal.Command{Path: path, Args: args, Size: terminal.Size{Width: 100, Height: 30}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sess.Close()
		_ = sess.Wait()
	})
	return sess
}

func waitConScreen(t *testing.T, sess terminal.Session, want string) *vt.Screen {
	t.Helper()
	return waitConScreenFor(t, sess, want, 20*time.Second)
}

func waitConScreenFor(t *testing.T, sess terminal.Session, want string, limit time.Duration) *vt.Screen {
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		s, err := sess.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(s.Text(), want) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("screen never showed %q:\n%s", want, s.Text())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestConPTY_OutputAndExit(t *testing.T) {
	sess := openConPTY(t, "cmd.exe", "/c", "echo hello-conpty & echo TERM=%TERM%")
	waitConScreen(t, sess, "hello-conpty")
	waitConScreen(t, sess, "TERM=xterm-256color")
	if err := sess.Wait(); err != nil {
		t.Fatalf("exit: %v", err)
	}
	failing := openConPTY(t, "cmd.exe", "/c", "exit 3")
	err := failing.Wait()
	var ec interface{ ExitCode() int }
	if !errors.As(err, &ec) || ec.ExitCode() != 3 {
		t.Fatalf("exit 3 reported as %v", err)
	}
}

func TestConPTY_InputResizeAndUnicode(t *testing.T) {
	sess := openConPTY(t, "powershell.exe", "-NoLogo", "-NoProfile", "-NoExit", "-Command", "Write-Output 'ready-ps'")
	// PowerShell's first start on a fresh machine can take a while (.NET
	// and module loading); CI saw it pass the 20 s used elsewhere.
	waitConScreenFor(t, sess, "ready-ps", 90*time.Second)
	if _, err := sess.Write([]byte("Write-Output ('typed-' + (2+3) + ' é✓')\r")); err != nil {
		t.Fatal(err)
	}
	waitConScreen(t, sess, "typed-5 é✓")
	if err := sess.Resize(terminal.Size{Width: 60, Height: 20}); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Write([]byte("Write-Output (\"cols=\" + $Host.UI.RawUI.WindowSize.Width)\r")); err != nil {
		t.Fatal(err)
	}
	waitConScreen(t, sess, "cols=60")
	if s, _ := sess.Snapshot(context.Background()); s.Cols != 60 || s.Rows != 20 {
		t.Fatalf("emulator size %dx%d", s.Cols, s.Rows)
	}
}

// TestConPTY_CloseStopsTheTree: closing a session ends the shell and what
// it started.
func TestConPTY_CloseStopsTheTree(t *testing.T) {
	sess := openConPTY(t, "cmd.exe", "/q", "/k", "echo shell-up")
	waitConScreen(t, sess, "shell-up")
	if _, err := sess.Write([]byte("ping -n 60 127.0.0.1\r")); err != nil {
		t.Fatal(err)
	}
	waitConScreen(t, sess, "Pinging")
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the session did not end after Close")
	}
}
