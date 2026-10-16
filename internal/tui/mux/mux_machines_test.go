//go:build !windows

package mux

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/tui/compositor"
)

// TestMachinesInTheSidebar runs the UI against one daemon with a second one
// as another machine: the sidebar lists that machine's agents, picking one
// shows its workspace at that agent, a copy made there reaches this
// machine's clipboard, and picking this machine again comes back.
func TestMachinesInTheSidebar(t *testing.T) {
	gpu := startDaemon(t)
	ctx := context.Background()
	if _, err := gpu.EnvironmentCreate(ctx, environment.CreateRequest{ID: "train", Root: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	created, err := client.NewWorkspace(gpu).TabCreate(ctx, pane.CreateTabRequest{
		EnvironmentID: "train", Name: "runs",
		Pane: pane.Spec{
			Name:    "trainer",
			Command: []string{"sh", "-c", `echo GPU-AGENT-SCREEN; read go; printf '\033]52;c;%s\a' ZnJvbSBncHU=; cat`},
			Env:     map[string]string{"HIVE_AGENT": "claude"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	h := newHarness(t, 120, 30, Options{Machines: []Machine{{Name: "gpu", Client: gpu}}})
	h.waitText("Machines")
	h.waitFor("gpu's agent in the sidebar", func() bool { return len(h.a.mach.agents["gpu"]) == 1 })
	h.waitText("trainer")
	if s := h.screen(); !strings.Contains(s, "▣ "+h.a.mach.here[:min(len(h.a.mach.here), 10)]) {
		t.Fatalf("this machine is not marked as shown:\n%s", s)
	}

	// Picking the agent shows gpu's workspace, at that agent.
	h.a.sidebarSelect(compositor.HitMachine, machineRowID("gpu", h.a.mach.agents["gpu"][0].ID))
	h.waitText("GPU-AGENT-SCREEN")
	if h.a.ws.envID != "train" || h.a.mach.name != "gpu" || !strings.Contains(h.a.WindowTitle(), "gpu") {
		t.Fatalf("after switching: env %q, machine %q, title %q", h.a.ws.envID, h.a.mach.name, h.a.WindowTitle())
	}
	if !slices.ContainsFunc(h.a.mach.others, func(m Machine) bool { return m.Name == "" }) {
		t.Fatal("this machine should now be among the others")
	}

	// A program there copies text: it lands on this machine's clipboard.
	if err := client.NewWorkspace(gpu).PaneRun(ctx, created.Pane.ID, "go"); err != nil {
		t.Fatal(err)
	}
	h.waitFor("the copy", func() bool {
		return slices.Contains(h.a.TakeOutput(), ansi.SetSystemClipboard("from gpu"))
	})

	// Back to this machine.
	h.a.sidebarSelect(compositor.HitMachine, machineRowID("", ""))
	h.waitFor("this machine's workspace", func() bool { return h.a.ws.snap != nil && h.a.ws.envID == "api" })
	h.waitText("trainer") // gpu's agent is listed again
}
