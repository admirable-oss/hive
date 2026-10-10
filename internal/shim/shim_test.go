package shim_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/admirable-oss/hive/internal/jsonfile"
	"github.com/admirable-oss/hive/internal/shim"
	"github.com/admirable-oss/hive/internal/terminal"
	"github.com/admirable-oss/hive/internal/vt"
)

// TestMain doubles as the shim binary: launchers in these tests run the
// test executable with HIVE_SHIM_TEST_EXEC=1 and the shim's directory.
func TestMain(m *testing.M) {
	if os.Getenv("HIVE_SHIM_TEST_EXEC") == "1" {
		shim.LingerAfterExit = 3 * time.Second
		os.Exit(shim.Main(os.Args[len(os.Args)-1]))
	}
	goleak.VerifyTestMain(m)
}

func newLauncher(t *testing.T) *shim.Launcher {
	t.Helper()
	// Short path: shim sockets must fit the unix socket limit.
	root, err := os.MkdirTemp("", "hs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return &shim.Launcher{
		Exe: os.Args[0],
		// Race-enabled binaries sleep a second at exit by default, which
		// would keep shims (and their reapers) alive past the leak check.
		Env:         []string{"HIVE_SHIM_TEST_EXEC=1", "GORACE=atexit_sleep_ms=0"},
		RunDir:      filepath.Join(root, "run"),
		StopGrace:   time.Second,
		DefaultSize: terminal.Size{Width: 60, Height: 12},
	}
}

func openAgent(t *testing.T, l *shim.Launcher, id, script string) *shim.Remote {
	t.Helper()
	sess, err := l.Open(context.Background(), terminal.Command{
		ID: id, Path: "sh", Args: []string{"-c", script}, LogPath: filepath.Join(l.RunDir, "..", id+".log"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return sess.(*shim.Remote)
}

// checkShimStderr fails the test if the shim reported a data race (the
// shims are race-enabled children of the race-enabled test binary).
func checkShimStderr(t *testing.T, l *shim.Launcher, id string) {
	t.Helper()
	if data, _ := os.ReadFile(filepath.Join(l.Dir(id), "shim.stderr")); strings.Contains(string(data), "DATA RACE") {
		t.Fatalf("shim %s reported a data race:\n%s", id, data)
	}
}

// stop ends an agent and its shim at the end of a test.
func stop(t *testing.T, l *shim.Launcher, r *shim.Remote) {
	t.Helper()
	_ = r.Close()
	done := make(chan struct{})
	go func() { _ = r.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Error("agent did not stop")
	}
	release(t, l, r)
}

// release releases r and waits for its shim process to exit, so no shim
// (or the launcher's reaper of it) outlives the test.
func release(t *testing.T, l *shim.Launcher, r *shim.Remote) {
	t.Helper()
	var st shim.State
	_ = jsonfile.Read(filepath.Join(l.Dir(r.ID()), "state.json"), &st)
	r.Release()
	waitProcessGone(t, st.ShimPID, r.ID())
}

func waitProcessGone(t *testing.T, pid int, id string) {
	t.Helper()
	if pid <= 0 {
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("shim %s (pid %d) did not exit after release", id, pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitScreen(t *testing.T, s terminal.Session, want string) *vt.Screen {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		scr, err := s.Snapshot(context.Background())
		if err == nil && strings.Contains(scr.Text(), want) {
			return scr
		}
		if time.Now().After(deadline) {
			t.Fatalf("screen never showed %q (last err %v)", want, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestShim_TerminalAgentLifecycle(t *testing.T) {
	l := newLauncher(t)
	r := openAgent(t, l, "a1", `echo hello; read x; echo "got $x"; stty size; read _`)
	if r.Pid() == 0 {
		t.Fatal("agent PID unknown")
	}
	waitScreen(t, r, "hello")
	if err := r.Resize(terminal.Size{Width: 70, Height: 15}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write([]byte("abc\n")); err != nil {
		t.Fatal(err)
	}
	scr := waitScreen(t, r, "15 70")
	if !strings.Contains(scr.Text(), "got abc") || r.Size() != (terminal.Size{Width: 70, Height: 15}) {
		t.Fatalf("screen:\n%s\nsize %+v", scr.Text(), r.Size())
	}

	// Frames stream from the shim reproduces the screen.
	ctx, cancel := context.WithCancel(context.Background())
	client := &vt.Screen{}
	done := make(chan error, 1)
	go func() {
		done <- r.Frames(ctx, func(f *vt.Frame) error {
			client.Apply(f)
			cancel() // one keyframe is enough here
			return nil
		})
	}()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if client.Text() != scr.Text() {
		t.Fatalf("frame screen differs:\n%s\nwant:\n%s", client.Text(), scr.Text())
	}

	checkShimStderr(t, l, "a1")
	stop(t, l, r)
	if _, err := os.Stat(l.Dir("a1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("release should remove the shim directory: %v", err)
	}
}

func TestShim_ScrollbackCrossesTheShim(t *testing.T) {
	l := newLauncher(t)
	// 40 lines on a 12-row screen: the first ones scroll into history.
	r := openAgent(t, l, "sb", `i=0; while [ $i -lt 40 ]; do echo "history $i"; i=$((i+1)); done; echo end; while :; do sleep 1; done`)
	waitScreen(t, r, "end")
	lines, err := r.Read(context.Background(), terminal.ReadRequest{Source: terminal.SourceHistory, Lines: 3})
	if err != nil {
		t.Fatal(err)
	}
	// The screen shows the last rows; the three newest scrolled-off lines
	// are those just above them.
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "history ") {
		t.Fatalf("scrollback = %q", lines)
	}
	scr, _ := r.Snapshot(context.Background())
	if first := scr.LineText(0); !strings.HasSuffix(lines[2], "history "+fmt.Sprint(atoi(strings.TrimPrefix(first, "history "))-1)) {
		t.Fatalf("newest scrollback line %q does not precede the screen's first line %q", lines[2], first)
	}
	checkShimStderr(t, l, "sb")
	stop(t, l, r)
}

func TestShim_WaitOutputAcrossTheShim(t *testing.T) {
	l := newLauncher(t)
	r := openAgent(t, l, "wo", `echo ready; read _; echo "tests: 42 passed"; read _`)
	waitScreen(t, r, "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan string, 1)
	go func() {
		line, err := r.WaitOutput(ctx, terminal.WaitRequest{Pattern: `\d+ passed`})
		if err != nil {
			t.Errorf("wait: %v", err)
		}
		done <- line
	}()
	time.Sleep(100 * time.Millisecond)
	_, _ = r.Write([]byte("\n"))
	if got := <-done; got != "tests: 42 passed" {
		t.Fatalf("matched %q", got)
	}

	short, cancelShort := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancelShort()
	if _, err := r.WaitOutput(short, terminal.WaitRequest{Pattern: "never"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout through the shim: %v", err)
	}
	checkShimStderr(t, l, "wo")
	stop(t, l, r)
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func TestShim_AgentSurvivesTheDaemonAndIsAdopted(t *testing.T) {
	l := newLauncher(t)
	r := openAgent(t, l, "a2", `echo "pid $$"; while :; do sleep 1; done`)
	before := waitScreen(t, r, "pid")
	pid := r.Pid()

	r.Detach() // the daemon goes away; the shim and agent do not
	if err := r.Wait(); !errors.Is(err, shim.ErrDetached) {
		t.Fatalf("Wait after Detach = %v, want ErrDetached", err)
	}
	if syscall.Kill(pid, 0) != nil {
		t.Fatal("agent died with the daemon's connection")
	}

	adopted, err := l.Connect(context.Background(), "a2")
	if err != nil {
		t.Fatal(err)
	}
	if adopted.Pid() != pid {
		t.Fatalf("adopted PID %d, want %d", adopted.Pid(), pid)
	}
	after, err := adopted.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.Text() != before.Text() {
		t.Fatalf("screen changed across adoption:\n%s\nwant:\n%s", after.Text(), before.Text())
	}
	checkShimStderr(t, l, "a2")
	stop(t, l, adopted)
}

func TestShim_ExitWhileNoDaemonIsWatching(t *testing.T) {
	l := newLauncher(t)
	r := openAgent(t, l, "a3", `sleep 0.3; exit 3`)
	var st0 shim.State
	_ = jsonfile.Read(filepath.Join(l.Dir("a3"), "state.json"), &st0)
	r.Detach()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var st shim.State
		if jsonfile.Read(filepath.Join(l.Dir("a3"), "state.json"), &st) == nil && st.Status == shim.StatusExited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shim never recorded the exit")
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, err := l.Connect(context.Background(), "a3")
	exit, ok := errors.AsType[*shim.Exit](err)
	if !ok || exit.Code != 3 {
		t.Fatalf("Connect = %v, want exit code 3", err)
	}
	waitProcessGone(t, st0.ShimPID, "a3") // Connect released the lingering shim
	if err := l.Discard("a3"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Connect(context.Background(), "a3"); !errors.Is(err, shim.ErrNoShim) {
		t.Fatalf("after Discard: %v, want ErrNoShim", err)
	}
}

func TestShim_KilledShimIsReportedGone(t *testing.T) {
	l := newLauncher(t)
	r := openAgent(t, l, "a4", `echo up; while :; do sleep 1; done`)
	waitScreen(t, r, "up")
	var st shim.State
	if err := jsonfile.Read(filepath.Join(l.Dir("a4"), "state.json"), &st); err != nil {
		t.Fatal(err)
	}
	agent := r.Pid()
	if err := syscall.Kill(st.ShimPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if err := r.Wait(); !errors.Is(err, shim.ErrShimGone) {
		t.Fatalf("Wait = %v, want ErrShimGone", err)
	}
	if _, err := l.Connect(context.Background(), "a4"); !errors.Is(err, shim.ErrShimGone) {
		t.Fatalf("Connect = %v, want ErrShimGone", err)
	}
	// The agent lost its terminal and received SIGHUP; make sure it is gone.
	_ = syscall.Kill(-agent, syscall.SIGKILL)
	r.Release()
}

func TestShim_ExitCodesReachWait(t *testing.T) {
	l := newLauncher(t)
	r := openAgent(t, l, "a5", `exit 7`)
	exit, ok := errors.AsType[*shim.Exit](r.Wait())
	if !ok || exit.ExitCode() != 7 {
		t.Fatalf("Wait = %v, want exit code 7", r.Wait())
	}
	checkShimStderr(t, l, "a5")
	release(t, l, r)
}

func TestShim_PlainAgent(t *testing.T) {
	l := newLauncher(t)
	out := filepath.Join(l.RunDir, "..", "plain.out")
	r, err := l.StartPlain(context.Background(), shim.Spec{
		ID: "p1", Path: "sh", Args: []string{"-c", "echo plain-out; echo plain-err >&2"},
		StdoutPath: out, StderrPath: out + ".err",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Wait(); err != nil {
		t.Fatalf("Wait = %v", err)
	}
	if _, err := r.Write([]byte("x")); !errors.Is(err, shim.ErrNotTerminal) {
		t.Fatalf("Write on a plain agent = %v", err)
	}
	checkShimStderr(t, l, "p1")
	release(t, l, r)
	if data, _ := os.ReadFile(out); string(data) != "plain-out\n" {
		t.Fatalf("stdout = %q", data)
	}
	if data, _ := os.ReadFile(out + ".err"); string(data) != "plain-err\n" {
		t.Fatalf("stderr = %q", data)
	}
}

func TestShim_StartFailures(t *testing.T) {
	l := newLauncher(t)
	_, err := l.Open(context.Background(), terminal.Command{ID: "f1", Path: "/no/such/agent"})
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("missing binary: %v", err)
	}
	if _, err := os.Stat(l.Dir("f1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a failed start must not leave its directory behind")
	}
}

// A storage root too deep for a unix socket address still works: the
// socket moves to a short private directory.
func TestShim_DeepRunDir(t *testing.T) {
	l := newLauncher(t)
	l.RunDir = filepath.Join(l.RunDir, strings.Repeat("d", 60), strings.Repeat("e", 60))
	r := openAgent(t, l, "deep", "echo deep-ok")
	if err := r.Wait(); err != nil {
		t.Fatalf("Wait = %v", err)
	}
	release(t, l, r)
}

func TestShim_HyperlinksCrossTheShim(t *testing.T) {
	l := newLauncher(t)
	r := openAgent(t, l, "links", `printf '\033]8;;https://example.com/x\007open me\033]8;;\007'; read _`)
	waitScreen(t, r, "open me")
	ctx, cancel := context.WithCancel(context.Background())
	got := &vt.Screen{}
	done := make(chan error, 1)
	go func() {
		done <- r.Frames(ctx, func(f *vt.Frame) error {
			got.Apply(f)
			cancel()
			return nil
		})
	}()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if link := got.Lines[0][0].Link; link != "https://example.com/x" {
		t.Fatalf("the daemon's view of the agent lost the link: %q", link)
	}
	stop(t, l, r)
}

func TestShim_ForegroundCrossesTheShim(t *testing.T) {
	l := newLauncher(t)
	r := openAgent(t, l, "fg", `echo up; exec sleep 30`) // sh -c has no job control: exec puts sleep in front
	waitScreen(t, r, "up")
	deadline := time.Now().Add(5 * time.Second)
	for {
		fg, err := r.Foreground(context.Background())
		if err == nil && len(fg.Args) > 0 && fg.Args[0] == "sleep" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("foreground = %+v, %v; want sleep", fg, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop(t, l, r)
}
