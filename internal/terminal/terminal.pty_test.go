package terminal_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/terminal"
)

func openPTY(t *testing.T, f terminal.PTYFactory, cmd terminal.Command) terminal.Session {
	t.Helper()
	sess, err := f.Open(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sess.Close()
		_ = sess.Wait()
	})
	return sess
}

// collect reads sub until it contains want or the deadline passes.
func collect(t *testing.T, sub *terminal.Subscription, want string) string {
	t.Helper()
	got := append([]byte(nil), sub.History...)
	deadline := time.After(5 * time.Second)
	for !bytes.Contains(got, []byte(want)) {
		select {
		case chunk, ok := <-sub.C:
			if !ok {
				t.Fatalf("stream ended before %q; got %q", want, got)
			}
			got = append(got, chunk...)
		case <-deadline:
			t.Fatalf("timed out waiting for %q; got %q", want, got)
		}
	}
	return string(got)
}

func TestPTY_OutputReachesSubscriberHistoryAndLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "out.log")
	sess := openPTY(t, terminal.PTYFactory{}, terminal.Command{
		Path: "sh", Args: []string{"-c", "echo hello-pty; read _"}, LogPath: logPath,
	})
	sub := sess.Subscribe()
	defer sub.Close()
	collect(t, sub, "hello-pty")

	// A late subscriber gets what it missed as history.
	late := sess.Subscribe()
	defer late.Close()
	if !bytes.Contains(late.History, []byte("hello-pty")) {
		t.Fatalf("history %q should contain earlier output", late.History)
	}

	if _, err := sess.Write([]byte("\n")); err != nil { // lets the shell exit
		t.Fatal(err)
	}
	if err := sess.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	waitClosed(t, sub)

	data, err := os.ReadFile(logPath)
	if err != nil || !bytes.Contains(data, []byte("hello-pty")) {
		t.Fatalf("log = %q, %v", data, err)
	}
	if info, _ := os.Stat(logPath); info.Mode().Perm() != 0o600 {
		t.Fatalf("log mode %v, want 0600", info.Mode().Perm())
	}
}

func TestPTY_InputAndResize(t *testing.T) {
	sess := openPTY(t, terminal.PTYFactory{}, terminal.Command{
		Path: "sh", Args: []string{"-c", "read line; echo got:$line; stty size; read _"},
		Size: terminal.Size{Width: 80, Height: 24},
	})
	sub := sess.Subscribe()
	defer sub.Close()
	if err := sess.Resize(terminal.Size{Width: 100, Height: 30}); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Write([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	out := collect(t, sub, "30 100")
	if !strings.Contains(out, "got:ping") {
		t.Fatalf("input did not reach the process: %q", out)
	}
}

func TestPTY_DefaultSizeComesFromFactory(t *testing.T) {
	sess := openPTY(t, terminal.PTYFactory{Size: terminal.Size{Width: 132, Height: 43}}, terminal.Command{
		Path: "sh", Args: []string{"-c", "stty size; read _"},
	})
	sub := sess.Subscribe()
	defer sub.Close()
	collect(t, sub, "43 132")
}

func TestPTY_CloseEscalatesAfterGrace(t *testing.T) {
	sess := openPTY(t, terminal.PTYFactory{StopGrace: 100 * time.Millisecond}, terminal.Command{
		Path: "sh", Args: []string{"-c", `trap "" TERM HUP; echo ready; while :; do sleep 1; done`},
	})
	sub := sess.Subscribe()
	defer sub.Close()
	collect(t, sub, "ready")

	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil { // idempotent
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = sess.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("process ignoring SIGTERM was not killed after the grace period")
	}
}

func TestPTY_SlowSubscriberIsCutOffNotCorrupted(t *testing.T) {
	// About 4 MiB of output: far more than a subscriber may buffer.
	sess := openPTY(t, terminal.PTYFactory{}, terminal.Command{
		Path: "sh", Args: []string{"-c", "head -c 4000000 /dev/zero | tr '\\0' x; echo; echo done"},
	})
	slow := sess.Subscribe() // never read until the process is finished
	defer slow.Close()
	_ = sess.Wait()
	waitClosed(t, slow)
	if !slow.Lagged() {
		t.Fatal("a subscriber that fell behind must be reported as lagged")
	}

	// The session itself is unaffected: a new subscriber sees the end.
	after := sess.Subscribe()
	defer after.Close()
	if !bytes.Contains(after.History, []byte("done")) {
		t.Fatalf("history tail %q should contain the final output", after.History[max(0, len(after.History)-64):])
	}
	if len(after.History) > terminal.DefaultHistoryBytes {
		t.Fatalf("history is %d bytes, over the %d-byte limit", len(after.History), terminal.DefaultHistoryBytes)
	}
}

func TestPTY_SubscribeAfterEnd(t *testing.T) {
	sess := openPTY(t, terminal.PTYFactory{}, terminal.Command{Path: "sh", Args: []string{"-c", "echo bye"}})
	first := sess.Subscribe()
	_ = sess.Wait()
	waitClosed(t, first)

	sub := sess.Subscribe()
	if _, ok := <-sub.C; ok {
		t.Fatal("subscribing to a finished session must yield a closed channel")
	}
	if sub.Lagged() {
		t.Fatal("a finished session is not a lag")
	}
	sub.Close()
	sub.Close() // idempotent
}

// waitClosed drains sub until its channel is closed.
func waitClosed(t *testing.T, sub *terminal.Subscription) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case _, ok := <-sub.C:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("subscription was not closed")
		}
	}
}
