package terminal_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/terminal"
	"github.com/admirable-oss/hive/internal/vt"
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

// waitScreen polls the session's screen until it contains want.
func waitScreen(t *testing.T, sess terminal.Session, want string) *vt.Screen {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
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
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPTY_ScreenLogAndEnvironment(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "out.log")
	sess := openPTY(t, terminal.PTYFactory{}, terminal.Command{
		Path: "sh", Args: []string{"-c", `printf 'hello-pty TERM=%s\n' "$TERM"; read _`}, LogPath: logPath,
	})
	waitScreen(t, sess, "hello-pty TERM=xterm-256color")

	if _, err := sess.Write([]byte("\n")); err != nil { // lets the shell exit
		t.Fatal(err)
	}
	if err := sess.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(data), "hello-pty") {
		t.Fatalf("log = %q, %v", data, err)
	}
	if info, _ := os.Stat(logPath); info.Mode().Perm() != 0o600 {
		t.Fatalf("log mode %v, want 0600", info.Mode().Perm())
	}
	// The final screen stays readable after the process is gone.
	if s, _ := sess.Snapshot(context.Background()); !strings.Contains(s.Text(), "hello-pty") {
		t.Fatalf("final screen lost:\n%s", s.Text())
	}
}

func TestPTY_InputAndResize(t *testing.T) {
	sess := openPTY(t, terminal.PTYFactory{}, terminal.Command{
		Path: "sh", Args: []string{"-c", "read line; echo got:$line; stty size; read _"},
		Size: terminal.Size{Width: 80, Height: 24},
	})
	if err := sess.Resize(terminal.Size{Width: 100, Height: 30}); err != nil {
		t.Fatal(err)
	}
	if got := sess.Size(); got != (terminal.Size{Width: 100, Height: 30}) {
		t.Fatalf("size = %+v", got)
	}
	if _, err := sess.Write([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	s := waitScreen(t, sess, "30 100")
	if !strings.Contains(s.Text(), "got:ping") || s.Cols != 100 || s.Rows != 30 {
		t.Fatalf("screen %dx%d:\n%s", s.Cols, s.Rows, s.Text())
	}
}

func TestSizeOrFillsEachDimension(t *testing.T) {
	got := terminal.Size{Height: 5}.Or(terminal.Size{Width: 132}, terminal.DefaultSize)
	if got != (terminal.Size{Width: 132, Height: 5}) {
		t.Fatalf("got %+v, want 132x5", got)
	}
}

func TestPTY_DefaultSizeComesFromFactory(t *testing.T) {
	sess := openPTY(t, terminal.PTYFactory{Size: terminal.Size{Width: 132, Height: 43}}, terminal.Command{
		Path: "sh", Args: []string{"-c", "stty size; read _"},
	})
	waitScreen(t, sess, "43 132")
}

// TestPTY_TerminalQueriesAreAnswered runs a program that asks the terminal
// for the cursor position and waits for the answer, as vim and Ink-based
// agents do at start-up. Without an emulator answering, it would hang.
func TestPTY_TerminalQueriesAreAnswered(t *testing.T) {
	script := `stty raw -echo; printf '\033[5;9H\033[6n'; reply=$(dd bs=1 count=6 2>/dev/null); stty sane; printf '\r\nreply:%s\n' "$(printf '%s' "$reply" | tr -d '\033')"; read _`
	sess := openPTY(t, terminal.PTYFactory{}, terminal.Command{Path: "sh", Args: []string{"-c", script}, Size: terminal.Size{Width: 40, Height: 10}})
	waitScreen(t, sess, "reply:[5;9R")
}

func TestPTY_CloseEscalatesAfterGrace(t *testing.T) {
	sess := openPTY(t, terminal.PTYFactory{StopGrace: 100 * time.Millisecond}, terminal.Command{
		Path: "sh", Args: []string{"-c", `trap "" TERM HUP; echo ready; while :; do sleep 1; done`},
	})
	waitScreen(t, sess, "ready")
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

// TestPTY_SlowViewerGetsCoalescedFramesAndTheExactScreen floods the terminal
// while a viewer blocks. The viewer must receive few frames, and applying
// them must reproduce the final screen exactly.
func TestPTY_SlowViewerGetsCoalescedFramesAndTheExactScreen(t *testing.T) {
	sess := openPTY(t, terminal.PTYFactory{Size: terminal.Size{Width: 60, Height: 15}}, terminal.Command{
		Path: "sh", Args: []string{"-c", `i=0; while [ $i -lt 3000 ]; do printf '\033[3%dmline %d\033[0m\n' $((i%7)) $i; i=$((i+1)); done; echo done; read _`},
	})
	var (
		mu     sync.Mutex
		client = &vt.Screen{}
		frames int
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- sess.Frames(ctx, func(f *vt.Frame) error {
			time.Sleep(20 * time.Millisecond) // a client on a slow link
			mu.Lock()
			client.Apply(f)
			frames++
			mu.Unlock()
			return nil
		})
	}()
	want := waitScreen(t, sess, "done")
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		same := client.Text() == want.Text()
		n := frames
		mu.Unlock()
		if same {
			if n > 1000 {
				t.Fatalf("%d frames for 3000 lines: frames were not coalesced", n)
			}
			break
		}
		if time.Now().After(deadline) {
			mu.Lock()
			t.Fatalf("viewer never caught up:\nviewer:\n%s\nterminal:\n%s", client.Text(), want.Text())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPTY_FramesEndWithTheProcess(t *testing.T) {
	sess := openPTY(t, terminal.PTYFactory{}, terminal.Command{Path: "sh", Args: []string{"-c", "echo bye"}})
	var last *vt.Frame
	err := sess.Frames(context.Background(), func(f *vt.Frame) error { last = f; return nil })
	if err != nil {
		t.Fatal(err)
	}
	s := &vt.Screen{}
	s.Apply(last)
	if !strings.Contains(s.Text(), "bye") {
		// The final frame may be a delta; replay a fresh view to check.
		var all *vt.Screen
		_ = sess.Frames(context.Background(), func(f *vt.Frame) error {
			if all == nil {
				all = &vt.Screen{}
			}
			all.Apply(f)
			return nil
		})
		if all == nil || !strings.Contains(all.Text(), "bye") {
			t.Fatal("frames of a finished session must show its final screen")
		}
	}
}

func TestPTY_ReadSources(t *testing.T) {
	sess := openPTY(t, terminal.PTYFactory{Size: terminal.Size{Width: 20, Height: 4}}, terminal.Command{
		Path: "sh", Args: []string{"-c", `for i in 1 2 3 4 5 6; do echo "line $i"; done; printf '%s' "$(printf 'w%.0s' $(seq 1 25))"; echo; read _`},
	})
	waitScreen(t, sess, "wwwww")
	ctx := context.Background()
	read := func(req terminal.ReadRequest) []string {
		t.Helper()
		lines, err := sess.Read(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		return lines
	}
	if got := read(terminal.ReadRequest{}); len(got) != 3 || got[0] != "line 6" {
		t.Fatalf("visible = %q", got)
	}
	if got := read(terminal.ReadRequest{Source: terminal.SourceHistory, Lines: 2}); strings.Join(got, ",") != "line 4,line 5" {
		t.Fatalf("history = %q", got)
	}
	if got := read(terminal.ReadRequest{Source: terminal.SourceRecent, Lines: 4}); strings.Join(got, ",") != "line 5,line 6,"+strings.Repeat("w", 20)+",wwwww" {
		t.Fatalf("recent = %q", got)
	}
	if got := read(terminal.ReadRequest{Source: terminal.SourceRecentUnwrapped, Lines: 2}); strings.Join(got, ",") != "line 6,"+strings.Repeat("w", 25) {
		t.Fatalf("recent-unwrapped = %q", got)
	}
	if _, err := sess.Read(ctx, terminal.ReadRequest{Source: "everything"}); !errors.Is(err, terminal.ErrInvalidRead) {
		t.Fatalf("unknown source: %v", err)
	}
}

// TestPTY_WaitOutputMissesNothing prints the line it waits for in the middle
// of a burst that scrolls it off the screen at once: frames would skip it,
// the wait must not.
func TestPTY_WaitOutputMissesNothing(t *testing.T) {
	sess := openPTY(t, terminal.PTYFactory{Size: terminal.Size{Width: 40, Height: 5}}, terminal.Command{
		Path: "sh", Args: []string{"-c", `echo ready; read _; i=0; while [ $i -lt 2000 ]; do echo "noise $i"; [ $i -eq 700 ] && echo "BUILD OK in 12s"; i=$((i+1)); done; read _`},
	})
	waitScreen(t, sess, "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type result struct {
		line string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		line, err := sess.WaitOutput(ctx, terminal.WaitRequest{Pattern: `BUILD OK in \d+s`})
		done <- result{line, err}
	}()
	time.Sleep(50 * time.Millisecond) // the wait is registered
	if _, err := sess.Write([]byte("\n")); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil || r.line != "BUILD OK in 12s" {
		t.Fatalf("wait = %q, %v", r.line, r.err)
	}
}

func TestPTY_WaitOutputOnlyCountsNewOutputUnlessAnywhere(t *testing.T) {
	sess := openPTY(t, terminal.PTYFactory{}, terminal.Command{Path: "sh", Args: []string{"-c", `echo "prompt> "; read _; echo finished`}})
	waitScreen(t, sess, "prompt>")

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := sess.WaitOutput(ctx, terminal.WaitRequest{Pattern: "prompt>"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("text already on screen must not count by default: %v", err)
	}
	line, err := sess.WaitOutput(context.Background(), terminal.WaitRequest{Pattern: "prompt>", Anywhere: true})
	if err != nil || line != "prompt>" {
		t.Fatalf("anywhere = %q, %v", line, err)
	}
	if _, err := sess.WaitOutput(context.Background(), terminal.WaitRequest{Pattern: "("}); !errors.Is(err, terminal.ErrInvalidRead) {
		t.Fatalf("bad pattern: %v", err)
	}

	// The program ends without printing a match.
	done := make(chan error, 1)
	go func() {
		_, err := sess.WaitOutput(context.Background(), terminal.WaitRequest{Pattern: "never printed"})
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	_, _ = sess.Write([]byte("\n"))
	select {
	case err := <-done:
		if !errors.Is(err, terminal.ErrEnded) {
			t.Fatalf("got %v, want ErrEnded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a wait must end when the output does")
	}
}
