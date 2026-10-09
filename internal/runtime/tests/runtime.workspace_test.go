package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/runtime"
)

func TestRuntime_GitStatusFollowsTheRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "init")

	path := shortSock(t)
	mod := runtime.NewModule(runtime.Config{SocketPath: path, GitInterval: 100 * time.Millisecond})
	if err := mod.Service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mod.Service.Stop(context.Background()) })
	sub := mod.Events.Subscribe(64, event.EnvironmentGit)
	defer sub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	env, err := mod.Environments.Create(ctx, environment.CreateRequest{ID: "repo", Root: repo})
	if err != nil {
		t.Fatal(err)
	}
	if env.Git == nil || env.Git.Branch != "main" {
		t.Fatalf("a new environment reports its git status: %+v", env.Git)
	}

	// A checkout is noticed and announced.
	git("checkout", "-q", "-b", "feature")
	for {
		ev, err := sub.Next(ctx)
		if err != nil {
			t.Fatalf("no environment.git event: %v", err)
		}
		var data struct {
			ID  string `json:"id"`
			Git *struct {
				Branch string `json:"branch"`
			} `json:"git"`
		}
		if err := json.Unmarshal(ev.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data.ID == "repo" && data.Git != nil && data.Git.Branch == "feature" {
			break
		}
	}
	got, err := mod.Environments.Get(ctx, "repo")
	if err != nil || got.Git == nil || got.Git.Branch != "feature" {
		t.Fatalf("get after checkout: %+v, %v", got.Git, err)
	}
}

func TestRuntime_PopupClosesWhenItsCommandExits(t *testing.T) {
	path := shortSock(t)
	mod := start(t, path)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := mod.Environments.Create(ctx, environment.CreateRequest{ID: "dev"}); err != nil {
		t.Fatal(err)
	}
	ws := client.NewWorkspace(client.NewService(client.Config{SocketPath: path}))
	tab, err := ws.TabCreate(ctx, pane.CreateTabRequest{EnvironmentID: "dev", Pane: pane.Spec{Command: []string{"sh", "-c", "read x"}}})
	if err != nil {
		t.Fatal(err)
	}
	pop, err := ws.PanePopup(ctx, pane.PopupRequest{TabID: tab.Tab.ID, Spec: pane.Spec{Command: []string{"sh", "-c", "sleep 0.2"}}})
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, err := ws.PaneGet(ctx, pop.ID)
		if pe, ok := errors.AsType[*protocol.Error](err); ok && pe.Code == protocol.ErrorCodeNotFound {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("the popup stayed after its command exited")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The tab and its other pane are untouched.
	if _, err := ws.PaneGet(ctx, tab.Pane.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRuntime_DeletingAnEnvironmentDropsItsTabs(t *testing.T) {
	path := shortSock(t)
	mod := start(t, path)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	if _, err := mod.Environments.Create(ctx, environment.CreateRequest{ID: "dev", Root: root}); err != nil {
		t.Fatal(err)
	}
	ws := client.NewWorkspace(client.NewService(client.Config{SocketPath: path}))
	created, err := ws.TabCreate(ctx, pane.CreateTabRequest{EnvironmentID: "dev", Pane: pane.Spec{Command: []string{"sh", "-c", "read x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mod.Environments.Delete(ctx, "dev"); err != nil {
		t.Fatal(err)
	}
	// Re-creating it starts empty: no tab survives from before.
	if _, err := mod.Environments.Create(ctx, environment.CreateRequest{ID: "dev", Root: root}); err != nil {
		t.Fatal(err)
	}
	tabs, err := ws.TabList(ctx, "dev")
	if err != nil || len(tabs) != 0 {
		t.Fatalf("tabs after re-creating: %+v, %v", tabs, err)
	}
	if _, err := ws.PaneGet(ctx, created.Pane.ID); err == nil {
		t.Fatal("the old pane must be gone")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("a rooted environment's directory is kept: %v", err)
	}
}
