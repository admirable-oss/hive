// Command hive-desktop is Hive's desktop app: a window onto the Hive daemon
// (environments, agents and their terminals), a tray item that says who
// needs you, and notifications when an agent is blocked or done.
//
// It is a client of the daemon, like `hive ui`: agents keep running when
// it quits. The engine is package core; this file binds it to Wails
// windows, menus, the tray and the system.
package main

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/dock"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/admirable-oss/hive/desktop/internal/core"
)

//go:embed all:frontend/dist
var assets embed.FS

// Events the frontend listens to besides the hub's.
const (
	// eventFocus asks the frontend to show a pane ({"pane": id}).
	eventFocus = "hive:focus"
	// eventCommand runs a frontend command from a menu ({"command": name}).
	eventCommand = "hive:command"
)

type FocusRequest struct {
	Pane string `json:"pane"`
}

type CommandRequest struct {
	Command string `json:"command"`
}

func init() {
	application.RegisterEvent[core.Connection](core.EventConnection)
	application.RegisterEvent[core.Workspace](core.EventWorkspace)
	application.RegisterEvent[core.ScreenUpdate](core.EventScreen)
	application.RegisterEvent[core.Settings](core.EventSettings)
	application.RegisterEvent[FocusRequest](eventFocus)
	application.RegisterEvent[CommandRequest](eventCommand)
}

// desktop is the app around the hub.
type desktop struct {
	app     *application.App
	hub     *core.Hub
	main    *application.WebviewWindow
	tray    *application.SystemTray
	dock    *dock.DockService
	notes   *notifications.NotificationService
	log     *slog.Logger
	focused atomic.Int32 // windows of the app that have focus
	noNotes atomic.Bool  // the system refused notifications (an unbundled build)

	mu       sync.Mutex
	summary  string // what the tray shows, to rebuild it only on change
	askedFor bool   // notification permission was asked for
	// trayWS is the latest workspace for the tray worker; trayKick wakes
	// it. The hub never waits on the tray.
	trayWS   *core.Workspace
	trayKick chan struct{}
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	d := &desktop{log: log, dock: dock.New(), notes: notifications.New(), trayKick: make(chan struct{}, 1)}
	d.hub = core.New(core.Config{
		Home: os.Getenv("HIVE_HOME"), Session: os.Getenv("HIVE_SESSION"),
		LoginShell: true, Emitter: d, Notify: d.notify, Logger: log,
		Clipboard: func(text string) { d.app.Clipboard.SetText(text) },
		Focused:   func() bool { return d.focused.Load() > 0 },
	})
	svc := &HiveService{hub: d.hub, shell: d}

	d.app = application.New(application.Options{
		Name:        "Hive",
		Description: "Coding agents that keep working when you close the window",
		Services: []application.Service{
			application.NewService(&lifecycle{hub: d.hub}),
			application.NewService(svc),
			application.NewService(&optional{name: "notifications", svc: d.notes, log: log, failed: &d.noNotes}),
			application.NewService(&optional{name: "the dock badge", svc: d.dock, log: log, failed: new(atomic.Bool)}),
		},
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac: application.MacOptions{
			// Closing the window keeps Hive in the menu bar; Quit ends it.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.admirable-oss.hive.desktop",
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				d.show()
				for _, a := range data.Args {
					d.openLink(a)
				}
			},
		},
		Server: serverOptions(),
		Logger: log,
	})

	d.main = d.newWindow("main", "Hive", "/", 1280, 820)
	if !serverBuild {
		d.app.Menu.Set(d.menu())
		d.setupTray()
		go d.trayWorker()
	}
	d.notes.OnNotificationResponse(func(r notifications.NotificationResult) {
		if r.Error != nil {
			return
		}
		if pane, ok := r.Response.UserInfo["pane"].(string); ok {
			d.focusPane(pane)
		} else {
			d.show()
		}
	})
	d.app.Event.OnApplicationEvent(events.Common.ApplicationLaunchedWithUrl, func(e *application.ApplicationEvent) {
		d.openLink(e.Context().URL())
	})
	d.app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) { d.show() })
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "hive://") {
			link := a
			d.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { d.openLink(link) })
		}
	}

	if err := d.app.Run(); err != nil {
		log.Error("hive desktop failed", "err", err)
		os.Exit(1)
	}
}

// lifecycle starts the hub with the app and closes it on quit; agents
// keep running with the daemon.
type lifecycle struct{ hub *core.Hub }

func (l *lifecycle) ServiceStartup(context.Context, application.ServiceOptions) error {
	l.hub.Start()
	return nil
}

func (l *lifecycle) ServiceShutdown() error {
	l.hub.Close()
	return nil
}

// optional runs a Wails service without exposing its methods to the
// frontend (only HiveService is callable from there), and whose failure to
// start leaves the app running without it: notifications need a bundle
// identifier, which a build run outside its .app bundle (or a server
// build) does not have.
type optional struct {
	name string
	svc  interface {
		ServiceStartup(context.Context, application.ServiceOptions) error
		ServiceShutdown() error
	}
	log    *slog.Logger
	failed *atomic.Bool
}

func (o *optional) ServiceStartup(ctx context.Context, opts application.ServiceOptions) error {
	if err := o.svc.ServiceStartup(ctx, opts); err != nil {
		o.log.Warn("running without "+o.name, "err", err)
		o.failed.Store(true)
	}
	return nil
}

func (o *optional) ServiceShutdown() error {
	if o.failed.Load() {
		return nil
	}
	return o.svc.ServiceShutdown()
}

// newWindow opens a window on a page of the frontend. Closing it hides it
// (the main window) or closes it (a pane's own window).
func (d *desktop) newWindow(name, title, page string, w, h int) *application.WebviewWindow {
	win := d.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: name, Title: title, URL: page, Width: w, Height: h, MinWidth: 560, MinHeight: 360,
		BackgroundColour: application.NewRGB(0x0d, 0x11, 0x17),
		Mac: application.MacWindow{
			TitleBar:                application.MacTitleBarHiddenInset,
			InvisibleTitleBarHeight: 38,
		},
	})
	win.OnWindowEvent(events.Common.WindowFocus, func(*application.WindowEvent) { d.focused.Add(1) })
	win.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) {
		if d.focused.Add(-1) < 0 {
			d.focused.Store(0)
		}
	})
	if name == "main" {
		win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
			e.Cancel()
			win.Hide()
		})
	}
	return win
}

// Emit sends hub events to every window, and keeps the tray and the dock
// badge current.
func (d *desktop) Emit(name string, data any) {
	d.app.Event.Emit(name, data)
	if ws, ok := data.(core.Workspace); ok && name == core.EventWorkspace && !serverBuild {
		d.mu.Lock()
		d.trayWS = &ws
		d.mu.Unlock()
		select {
		case d.trayKick <- struct{}{}:
		default:
		}
	}
}

// trayWorker applies the latest workspace to the tray and the dock.
func (d *desktop) trayWorker() {
	for range d.trayKick {
		d.mu.Lock()
		ws := d.trayWS
		d.trayWS = nil
		d.mu.Unlock()
		if ws != nil {
			d.updateTray(*ws)
		}
	}
}

func (d *desktop) show() {
	d.main.Show()
	d.main.UnMinimise()
	d.main.Focus()
}

// focusPane brings the app forward on a pane.
func (d *desktop) focusPane(pane string) {
	d.show()
	if err := d.hub.FocusPane(pane); err != nil {
		d.log.Debug("focus pane", "pane", pane, "err", err)
	}
	d.app.Event.Emit(eventFocus, FocusRequest{Pane: pane})
}

// openLink follows a hive:// link: hive://pane/<id> or hive://agent/<id>.
func (d *desktop) openLink(link string) {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "hive" {
		return
	}
	id := strings.Trim(u.Path, "/")
	switch u.Host {
	case "pane":
		d.focusPane(id)
	case "agent":
		for _, a := range d.hub.Workspace().Agents {
			if a.ID == id && a.PaneID != "" {
				d.focusPane(a.PaneID)
				return
			}
		}
		d.show()
	default:
		d.show()
	}
}

// notify posts a system notification; on macOS it asks for permission the
// first time.
func (d *desktop) notify(n core.Notification) {
	if d.noNotes.Load() {
		return
	}
	if runtime.GOOS == "darwin" {
		d.mu.Lock()
		ask := !d.askedFor
		d.askedFor = true
		d.mu.Unlock()
		if ok, err := d.notes.CheckNotificationAuthorization(); err == nil && !ok {
			if !ask {
				return
			}
			if ok, err := d.notes.RequestNotificationAuthorization(); err != nil || !ok {
				return
			}
		}
	}
	err := d.notes.SendNotification(notifications.NotificationOptions{
		ID: n.ID, Title: n.Title, Body: n.Body, Data: map[string]any{"pane": n.PaneID, "agent": n.AgentID},
	})
	if err != nil {
		d.log.Debug("notification failed", "err", err)
	}
}

// --- shell, for HiveService ---

func (d *desktop) openURL(u string) error { return d.app.Browser.OpenURL(u) }

func (d *desktop) setClipboard(text string) { d.app.Clipboard.SetText(text) }

func (d *desktop) clipboard() string {
	text, _ := d.app.Clipboard.Text()
	return text
}

// popOut shows a pane in a window of its own.
func (d *desktop) popOut(pane string) error {
	name := "pane-" + pane
	if w, ok := d.app.Window.GetByName(name); ok {
		w.Show()
		w.Focus()
		return nil
	}
	title := "Hive"
	for _, p := range d.hub.Workspace().Panes {
		if p.ID == pane && p.Name != "" {
			title = p.Name + " — Hive"
		}
	}
	d.newWindow(name, title, "/#/pane/"+url.PathEscape(pane), 900, 600).Show()
	return nil
}

// --- menus and tray ---

// accel is a shortcut that leaves the terminal its own keys: ⌘ on macOS,
// Ctrl+Shift elsewhere (Ctrl+D, Ctrl+W and Ctrl+T mean things to shells).
func accel(key string) string {
	if runtime.GOOS == "darwin" {
		return "CmdOrCtrl+" + key
	}
	return "Ctrl+Shift+" + key
}

func (d *desktop) command(name string) func(*application.Context) {
	return func(*application.Context) {
		d.show()
		d.app.Event.Emit(eventCommand, CommandRequest{Command: name})
	}
}

func (d *desktop) menu() *application.Menu {
	m := application.NewMenu()
	if runtime.GOOS == "darwin" {
		m.AddRole(application.AppMenu)
	}
	file := m.AddSubmenu("File")
	file.Add("New Agent…").SetAccelerator(accel("N")).OnClick(d.command("new-agent"))
	file.Add("New Tab").SetAccelerator(accel("T")).OnClick(d.command("new-tab"))
	file.Add("New Environment…").OnClick(d.command("new-environment"))
	file.AddSeparator()
	file.Add("Close Pane").SetAccelerator(accel("W")).OnClick(d.command("close-pane"))
	if runtime.GOOS != "darwin" {
		file.AddSeparator()
		file.Add("Quit Hive").SetAccelerator("Ctrl+Q").OnClick(func(*application.Context) { d.app.Quit() })
	}
	m.AddRole(application.EditMenu)
	view := m.AddSubmenu("View")
	view.Add("Command Palette…").SetAccelerator(accel("K")).OnClick(d.command("palette"))
	view.Add("Prompt Agent…").SetAccelerator(accel("P")).OnClick(d.command("prompt"))
	view.AddSeparator()
	view.Add("Split Right").SetAccelerator(accel("D")).OnClick(d.command("split-right"))
	view.Add("Split Down").SetAccelerator(accel("E")).OnClick(d.command("split-down"))
	view.Add("Zoom Pane").SetAccelerator(accel("Z")).OnClick(d.command("zoom"))
	view.Add("Open Pane in New Window").OnClick(d.command("pop-out"))
	view.AddSeparator()
	view.Add("Next Agent Needing You").SetAccelerator(accel("J")).OnClick(d.command("next-attention"))
	view.Add("Toggle Sidebar").SetAccelerator(accel("B")).OnClick(d.command("toggle-sidebar"))
	m.AddRole(application.WindowMenu)
	return m
}

func (d *desktop) setupTray() {
	d.tray = d.app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		d.tray.SetTemplateIcon(trayTemplate)
	} else {
		d.tray.SetIcon(trayAttention)
	}
	d.tray.SetTooltip("Hive")
	d.tray.SetMenu(d.trayMenu(core.Workspace{}))
}

// attention lists agents that need someone: blocked first, then done.
func attention(ws core.Workspace) []core.Agent {
	var out []core.Agent
	for _, state := range []string{"blocked", "done"} {
		for _, a := range ws.Agents {
			if a.State == state {
				out = append(out, a)
			}
		}
	}
	return out
}

func (d *desktop) trayMenu(ws core.Workspace) *application.Menu {
	m := application.NewMenu()
	m.Add("Open Hive").OnClick(func(*application.Context) { d.show() })
	m.AddSeparator()
	var parts []string
	for _, state := range []string{"blocked", "working", "done", "idle"} {
		if n := ws.Counts[state]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, state))
		}
	}
	if len(parts) == 0 {
		parts = []string{"No agents"}
	}
	m.Add(strings.Join(parts, " · ")).SetEnabled(false)
	agents := attention(ws)
	for i, a := range agents {
		if i == 8 {
			m.Add(fmt.Sprintf("and %d more…", len(agents)-i)).OnClick(func(*application.Context) { d.show() })
			break
		}
		pane := a.PaneID
		mark := map[string]string{"blocked": "● ", "done": "✓ "}[a.State]
		m.Add(mark + a.Name + " — " + a.EnvironmentID).OnClick(func(*application.Context) { d.focusPane(pane) })
	}
	m.AddSeparator()
	m.Add("New Agent…").OnClick(d.command("new-agent"))
	m.AddSeparator()
	m.Add("Quit Hive").OnClick(func(*application.Context) { d.app.Quit() })
	return m
}

// updateTray shows the agents that need someone in the tray and on the
// dock badge, rebuilding the menu only when they change.
func (d *desktop) updateTray(ws core.Workspace) {
	agents := attention(ws)
	keys := make([]string, 0, len(agents)+4)
	for _, a := range agents {
		keys = append(keys, a.ID+a.State+a.Name)
	}
	for _, s := range []string{"blocked", "working", "done", "idle"} {
		keys = append(keys, fmt.Sprint(ws.Counts[s]))
	}
	summary := strings.Join(keys, "|")
	d.mu.Lock()
	same := summary == d.summary
	d.summary = summary
	d.mu.Unlock()
	if same || d.tray == nil {
		return
	}
	blocked := ws.Counts["blocked"]
	label := ""
	if blocked > 0 {
		label = fmt.Sprint(blocked)
		_ = d.dock.SetBadge(label)
	} else {
		_ = d.dock.RemoveBadge()
	}
	if runtime.GOOS == "darwin" {
		d.tray.SetLabel(label)
	}
	tip := "Hive"
	if blocked > 0 {
		tip = fmt.Sprintf("Hive: %d agent%s need%s you", blocked, plural(blocked), map[bool]string{true: "s", false: ""}[blocked == 1])
	}
	d.tray.SetTooltip(tip)
	d.tray.SetMenu(d.trayMenu(ws))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
