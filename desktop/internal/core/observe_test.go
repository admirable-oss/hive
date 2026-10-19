package core

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestObserverWindow: a credential made for a window shows what its scope
// allows (screens only with Screens), live, and nothing once revoked.
func TestObserverWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a daemon")
	}
	if runtime.GOOS == "windows" {
		t.Skip("types POSIX shell commands")
	}
	home := testHome(t)
	h := New(Config{Home: home, Hive: hiveBin, Emitter: &recorder{}})
	h.Start()
	defer h.Close()
	waitFor(t, "a connection", 30*time.Second, func() bool { return h.Connection().State == StateConnected })
	for _, env := range []string{"shown", "hidden"} {
		if err := h.CreateEnvironment(env, t.TempDir()); err != nil {
			t.Fatal(err)
		}
	}
	pane, err := h.NewTab("shown")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.NewTab("hidden"); err != nil {
		t.Fatal(err)
	}

	if _, err := h.CreateObserver(ObserveRequest{Hours: 1}); err == nil {
		t.Fatal("an observer that may see nothing was made")
	}
	if _, err := h.CreateObserver(ObserveRequest{All: true, Hours: 500}); err == nil {
		t.Fatal("an observer for 500 hours was made")
	}

	obs, err := h.CreateObserver(ObserveRequest{Environments: []string{"shown"}, Screens: true, Hours: 1})
	if err != nil {
		t.Fatal(err)
	}
	if obs.token == "" || obs.ID == "" || obs.Scope != "shown" {
		t.Fatalf("observer %+v", obs)
	}
	win := &recorder{}
	o := Observe(obs, win.Emit, nil)
	defer o.Close()
	waitFor(t, "the observed workspace", 15*time.Second, func() bool {
		v := o.View()
		_, ok := paneByID(v.Workspace, pane)
		return v.State == StateConnected && ok
	})
	for _, e := range o.View().Workspace.Environments {
		if e.ID == "hidden" {
			t.Fatal("the observer sees an environment outside its scope")
		}
	}
	if err := o.Watch(pane); err != nil {
		t.Fatal(err)
	}
	screen := func() string {
		win.mu.Lock()
		defer win.mu.Unlock()
		var b strings.Builder
		for _, e := range win.events {
			if s, ok := e.data.(ObservedScreen); ok && s.Pane == pane {
				for _, l := range s.Lines {
					b.WriteString(l.Text + "\n")
				}
			}
		}
		return b.String()
	}
	waitFor(t, "the shell's prompt, observed", 15*time.Second, func() bool { return strings.TrimSpace(screen()) != "" })
	if err := h.OpenView("w", pane, 80, 24); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a view to type through", 10*time.Second, func() bool { return h.Input(pane, "") == nil && h.Input(pane, " ") == nil })
	if err := h.Input(pane, "\x15echo obs-$((6*7))\r"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the output, observed", 15*time.Second, func() bool { return strings.Contains(screen(), "obs-42") })

	// Revoked: the window's access ends.
	if err := h.RevokeObserver(obs.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the window's access to end", 30*time.Second, func() bool { return o.View().State == "ended" })

	// Without Screens, states only.
	meta, err := h.CreateObserver(ObserveRequest{All: true, Hours: 1})
	if err != nil {
		t.Fatal(err)
	}
	o2 := Observe(meta, (&recorder{}).Emit, nil)
	defer o2.Close()
	if err := o2.Watch(pane); err == nil {
		t.Fatal("an observer without screens may watch one")
	}
	_ = h.RevokeObserver(meta.ID)
}
