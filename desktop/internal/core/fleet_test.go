package core

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMergeWorkspaces(t *testing.T) {
	local := Workspace{
		Environments: []Environment{{ID: "api"}}, Tabs: []Tab{{ID: "t1", EnvironmentID: "api", Focused: "p1"}},
		Panes:  []Pane{{ID: "p1", TabID: "t1", EnvironmentID: "api", ProcessID: "x1"}},
		Agents: []Agent{{ID: "a1", PaneID: "p1", EnvironmentID: "api", State: "working"}}, State: "working",
		Counts: map[string]int{"working": 1},
	}
	gpu := Workspace{
		Environments: []Environment{{ID: "train"}}, Tabs: []Tab{{ID: "t1", EnvironmentID: "train", Focused: "p1"}},
		Panes:  []Pane{{ID: "p1", TabID: "t1", EnvironmentID: "train", ProcessID: "x1", Agent: "a1"}},
		Agents: []Agent{{ID: "a1", PaneID: "p1", TabID: "t1", EnvironmentID: "train", State: "blocked"}}, State: "blocked",
		Counts: map[string]int{"blocked": 1},
	}
	w := mergeWorkspaces([]string{"", "gpu"}, map[string]Workspace{"": local, "gpu": gpu})
	if len(w.Panes) != 2 || w.Panes[0].ID != "p1" || w.Panes[1].ID != "gpu/p1" || w.Panes[1].TabID != "gpu/t1" || w.Panes[1].Agent != "gpu/a1" {
		t.Fatalf("panes %+v", w.Panes)
	}
	if w.Tabs[1].Focused != "gpu/p1" || w.Environments[1].ID != "gpu/train" || w.Environments[1].Machine != "gpu" {
		t.Fatalf("tabs %+v envs %+v", w.Tabs, w.Environments)
	}
	if a := w.Agents[1]; a.ID != "gpu/a1" || a.PaneID != "gpu/p1" || a.Machine != "gpu" {
		t.Fatalf("agent %+v", a)
	}
	if w.State != "blocked" || w.Counts["blocked"] != 1 || w.Counts["working"] != 1 {
		t.Fatalf("rollup %q %v", w.State, w.Counts)
	}
}

func TestFleetRoutes(t *testing.T) {
	f := NewFleet(Config{})
	if h, raw, err := f.route("p1"); err != nil || h != f.local || raw != "p1" {
		t.Fatalf("a local ID: %v %q %v", h, raw, err)
	}
	if _, _, err := f.route("gpu/p1"); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("an ID of a machine not connected: %v", err)
	}
	var got []Notification
	f.cfg.Notify = func(n Notification) { got = append(got, n) }
	f.notifier("gpu")(Notification{ID: "n1", Title: "claude needs you", AgentID: "a1", PaneID: "p1"})
	if len(got) != 1 || got[0].PaneID != "gpu/p1" || got[0].AgentID != "gpu/a1" || got[0].Title != "gpu · claude needs you" {
		t.Fatalf("notification %+v", got)
	}
}

func TestPairRequestArgs(t *testing.T) {
	for _, bad := range []PairRequest{
		{},
		{ID: "x", Host: "y"},
		{ID: "not-an-id"},
		{Host: "-oProxyCommand=evil"},
		{Host: "a b"},
		{Host: "box", User: "-l"},
		{Host: "box", As: "../x"},
	} {
		if _, err := bad.args(); err == nil {
			t.Errorf("%+v is accepted", bad)
		}
	}
	args, err := PairRequest{ID: strings.Repeat("a", 26), User: "me", As: "gpu"}.args()
	if err != nil || strings.Join(args, " ") != strings.Repeat("a", 26)+" --user me --as gpu" {
		t.Fatalf("args %q %v", args, err)
	}
	h := New(Config{})
	if err := h.MachineAction(context.Background(), "gpu", "format"); err == nil {
		t.Fatal("an unknown action is accepted")
	}
	if err := h.Trust(context.Background(), "gpu", "nope"); err == nil {
		t.Fatal("trust without a confirmed identity is accepted")
	}
}

// TestFleetShowsMachines: a saved machine's daemon (here, a second Hive
// home) joins the workspace under its name; its panes are shown and typed
// into like local ones, and it leaves when the machine is disabled.
func TestFleetShowsMachines(t *testing.T) {
	if testing.Short() {
		t.Skip("starts daemons")
	}
	if runtime.GOOS == "windows" {
		t.Skip("types POSIX shell commands")
	}
	local, far := testHome(t), testHome(t)
	hive := func(home string, args ...string) string {
		t.Helper()
		cmd := exec.Command(hiveBin, args...)
		cmd.Env = append(os.Environ(), "HIVE_HOME="+home, "HIVE_CONFIG="+filepath.Join(home, "config.toml"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("hive %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	hive(local, "env", "create", "near", "--cwd", t.TempDir())
	hive(far, "env", "create", "train", "--cwd", t.TempDir())
	hive(far, "tab", "create", "train", "--name", "job")

	rec := &recorder{}
	f := NewFleet(Config{Home: local, Hive: hiveBin, Emitter: rec})
	var mu sync.Mutex
	machines := []SavedMachine{{Name: "gpu", Host: "gpu", State: "trusted"}}
	f.listMachines = func(context.Context) ([]SavedMachine, error) {
		mu.Lock()
		defer mu.Unlock()
		return append([]SavedMachine(nil), machines...), nil
	}
	f.dialMachine = func(string) func(context.Context) (net.Conn, error) {
		return func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", filepath.Join(far, "hive.sock"))
		}
	}
	f.Start()
	defer f.Close()

	var remote Pane
	waitFor(t, "both machines in the workspace", 30*time.Second, func() bool {
		w := f.Workspace()
		envs := map[string]bool{}
		for _, e := range w.Environments {
			envs[e.ID] = true
		}
		for _, p := range w.Panes {
			if strings.HasPrefix(p.ID, "gpu/") {
				remote = p
			}
		}
		return envs["near"] && envs["gpu/train"] && remote.ID != ""
	})
	if ms := f.Machines(); len(ms) != 1 || ms[0].Connection.State != StateConnected {
		t.Fatalf("machines %+v", ms)
	}
	if err := f.ResizeTab(remote.TabID, 80, 24); err != nil {
		t.Fatal(err)
	}
	if err := f.OpenView("w1", remote.ID, 80, 24); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the remote shell's prompt", 15*time.Second, func() bool { return strings.TrimSpace(rec.screen(remote.ID)) != "" })
	if err := f.Input(remote.ID, "echo far-$((3*14))\r"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "output from the machine", 15*time.Second, func() bool { return strings.Contains(rec.screen(remote.ID), "far-42") })

	pane, err := f.NewTab("gpu/train")
	if err != nil || !strings.HasPrefix(pane, "gpu/") {
		t.Fatalf("a tab on the machine: %q %v", pane, err)
	}

	mu.Lock()
	machines[0].Disabled, machines[0].State = true, "disabled"
	mu.Unlock()
	f.RefreshMachines()
	waitFor(t, "the machine to leave", 10*time.Second, func() bool {
		for _, e := range f.Workspace().Environments {
			if e.Machine != "" {
				return false
			}
		}
		return true
	})
	if _, _, err := f.route(remote.ID); err == nil {
		t.Fatal("a disabled machine's pane is still routed")
	}
}
