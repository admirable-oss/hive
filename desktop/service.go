package main

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/admirable-oss/hive/desktop/internal/core"
)

// HiveService is what the frontend calls. Every method is a thin, typed
// pass-through to the hub, which validates its input before anything
// reaches the daemon; nothing here runs commands or reads files.
type HiveService struct {
	hub   *core.Fleet
	shell shell
}

// shell is what the service needs from the desktop around it.
type shell interface {
	openURL(u string) error
	setClipboard(text string)
	clipboard() string
	popOut(paneID string) error
	popOutEnvironment(envID string) error
	updateStatus() UpdateStatus
	checkForUpdates(ctx context.Context) (UpdateStatus, error)
	installUpdate(ctx context.Context) error
	setUpdateChecks(on bool) error
	startAtLogin() (bool, error)
	setStartAtLogin(on bool) error
	openObserver(req core.ObserveRequest) (string, error)
}

// Connection is how the app is connected to the daemon.
func (s *HiveService) Connection(ctx context.Context) core.Connection {
	if guard(ctx) != nil {
		return core.Connection{}
	}
	return s.hub.Connection()
}

// Workspace is the daemon's environments, tabs, panes and agents.
func (s *HiveService) Workspace(ctx context.Context) core.Workspace {
	if guard(ctx) != nil {
		return core.Workspace{}
	}
	return s.hub.Workspace()
}

// Settings are the parts of the Hive configuration the app follows.
func (s *HiveService) Settings(ctx context.Context) core.Settings {
	if guard(ctx) != nil {
		return core.Settings{}
	}
	return s.hub.Settings()
}

// Retry connects again now.
func (s *HiveService) Retry(ctx context.Context) {
	if guard(ctx) != nil {
		return
	}
	s.hub.Retry()
}

// OpenView streams a pane's screen at cols × rows to viewer (a window's
// own ID).
func (s *HiveService) OpenView(ctx context.Context, viewer, paneID string, cols, rows int) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.OpenView(viewer, paneID, cols, rows)
}

// CloseView says viewer stopped showing a pane.
func (s *HiveService) CloseView(ctx context.Context, viewer, paneID string) {
	if guard(ctx) != nil {
		return
	}
	s.hub.CloseView(viewer, paneID)
}

// RefreshView sends a pane's whole screen again.
func (s *HiveService) RefreshView(ctx context.Context, paneID string) {
	if guard(ctx) != nil {
		return
	}
	s.hub.Refresh(paneID)
}

// Input types bytes into a pane.
func (s *HiveService) Input(ctx context.Context, paneID, data string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.Input(paneID, data)
}

// Paste pastes text into a pane (bracketed when the program asks).
func (s *HiveService) Paste(ctx context.Context, paneID, text string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.Paste(paneID, text)
}

// PasteClipboard pastes the clipboard into a pane.
func (s *HiveService) PasteClipboard(ctx context.Context, paneID string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.Paste(paneID, s.shell.clipboard())
}

// Copy puts text on the clipboard.
func (s *HiveService) Copy(ctx context.Context, text string) {
	if guard(ctx) != nil {
		return
	}
	s.shell.setClipboard(text)
}

// ResizeTab sets a tab's area in cells.
func (s *HiveService) ResizeTab(ctx context.Context, tabID string, cols, rows int) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.ResizeTab(tabID, cols, rows)
}

// FocusTab makes a tab active.
func (s *HiveService) FocusTab(ctx context.Context, tabID string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.FocusTab(tabID)
}

// NewTab opens a shell in a new tab and returns its pane.
func (s *HiveService) NewTab(ctx context.Context, envID string) (string, error) {
	if err := guard(ctx); err != nil {
		return "", err
	}
	return s.hub.NewTab(envID)
}

// CloseTab closes a tab and stops its processes.
func (s *HiveService) CloseTab(ctx context.Context, tabID string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.CloseTab(tabID)
}

// FocusPane focuses a pane.
func (s *HiveService) FocusPane(ctx context.Context, paneID string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.FocusPane(paneID)
}

// SplitPane splits a pane (left, right, up or down) and returns the new one.
func (s *HiveService) SplitPane(ctx context.Context, paneID, direction string) (string, error) {
	if err := guard(ctx); err != nil {
		return "", err
	}
	return s.hub.SplitPane(paneID, direction)
}

// ResizePane moves a pane's border.
func (s *HiveService) ResizePane(ctx context.Context, paneID, direction string, cells int) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.ResizePane(paneID, direction, cells)
}

// ZoomPane toggles a pane filling its tab.
func (s *HiveService) ZoomPane(ctx context.Context, paneID string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.ZoomPane(paneID)
}

// ClosePane closes a pane and stops its process.
func (s *HiveService) ClosePane(ctx context.Context, paneID string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.ClosePane(paneID)
}

// RenamePane renames a pane.
func (s *HiveService) RenamePane(ctx context.Context, paneID, name string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.RenamePane(paneID, name)
}

// PopOut opens a pane in a window of its own.
func (s *HiveService) PopOut(ctx context.Context, paneID string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.shell.popOut(paneID)
}

// SwapPane exchanges two panes of one tab: what dragging a pane onto
// another does.
func (s *HiveService) SwapPane(ctx context.Context, paneID, withID string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.SwapPane(paneID, withID)
}

// FocusNeighbour focuses the pane beside paneID that way (left, right, up
// or down).
func (s *HiveService) FocusNeighbour(ctx context.Context, paneID, dir string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.FocusNeighbour(paneID, dir)
}

// OpenEnvironmentWindow shows an environment, with its tabs, in a window
// of its own.
func (s *HiveService) OpenEnvironmentWindow(ctx context.Context, envID string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.shell.popOutEnvironment(envID)
}

// UpdateStatus says whether the app can update itself, and whether a newer
// release is available.
func (s *HiveService) UpdateStatus(ctx context.Context) (UpdateStatus, error) {
	if err := guard(ctx); err != nil {
		return UpdateStatus{}, err
	}
	return s.shell.updateStatus(), nil
}

// CheckForUpdates looks for a newer release now.
func (s *HiveService) CheckForUpdates(ctx context.Context) (UpdateStatus, error) {
	if err := guard(ctx); err != nil {
		return UpdateStatus{}, err
	}
	return s.shell.checkForUpdates(ctx)
}

// InstallUpdate installs the available release, after checking its
// signature and checksum, and restarts the app on it; agents keep running.
// If the new version does not start, the previous one is put back.
func (s *HiveService) InstallUpdate(ctx context.Context) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.shell.installUpdate(ctx)
}

// SetUpdateChecks turns the daily update check on or off.
func (s *HiveService) SetUpdateChecks(ctx context.Context, on bool) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.shell.setUpdateChecks(on)
}

// History returns a pane's scrollback and screen as text (its last lines).
func (s *HiveService) History(ctx context.Context, paneID string, lines int) ([]string, error) {
	if err := guard(ctx); err != nil {
		return nil, err
	}
	return s.hub.History(paneID, lines)
}

// StartAtLogin says whether the app starts when you log in.
func (s *HiveService) StartAtLogin(ctx context.Context) (bool, error) {
	if err := guard(ctx); err != nil {
		return false, err
	}
	return s.shell.startAtLogin()
}

// SetStartAtLogin makes the app start when you log in, or not.
func (s *HiveService) SetStartAtLogin(ctx context.Context, on bool) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.shell.setStartAtLogin(on)
}

// Manifests lists the agents a machine's Hive can start ("" for this
// computer).
func (s *HiveService) Manifests(ctx context.Context, machine string) ([]core.Manifest, error) {
	if err := guard(ctx); err != nil {
		return nil, err
	}
	return s.hub.Manifests(machine)
}

// StartAgent starts an agent and returns its pane.
func (s *HiveService) StartAgent(ctx context.Context, req core.StartAgentRequest) (string, error) {
	if err := guard(ctx); err != nil {
		return "", err
	}
	return s.hub.StartAgent(req)
}

// Prompt sends a prompt to an agent and returns how it reacted.
func (s *HiveService) Prompt(ctx context.Context, agentID, text string) (string, error) {
	if err := guard(ctx); err != nil {
		return "", err
	}
	return s.hub.Prompt(agentID, text)
}

// StopAgent stops an agent.
func (s *HiveService) StopAgent(ctx context.Context, agentID string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.StopAgent(agentID)
}

// ApproveTask lets a plan's task that waits for approval start; deny
// cancels it.
func (s *HiveService) ApproveTask(ctx context.Context, plan, task string, deny bool) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.ApproveTask(plan, task, deny)
}

// CancelPlan cancels a plan.
func (s *HiveService) CancelPlan(ctx context.Context, plan string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.CancelPlan(plan)
}

// Insights returns a machine's time and usage today ("" for this
// computer): by kind, the latest asks, and the budgets.
func (s *HiveService) Insights(ctx context.Context, machine string) (core.Insights, error) {
	if err := guard(ctx); err != nil {
		return core.Insights{}, err
	}
	return s.hub.Insights(machine)
}

// ResumeBudget lets new tasks start again under a budget that holds them,
// until its window ends: for one key, or every held one when key is "".
func (s *HiveService) ResumeBudget(ctx context.Context, machine, name, key string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.ResumeBudget(machine, name, key)
}

// CancelWorkflowRun cancels a workflow run.
func (s *HiveService) CancelWorkflowRun(ctx context.Context, id string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.CancelWorkflowRun(id)
}

// RerunWorkflow runs a workflow run again, with its recorded text, inputs
// and trigger, and returns the new run's ID.
func (s *HiveService) RerunWorkflow(ctx context.Context, id string) (string, error) {
	if err := guard(ctx); err != nil {
		return "", err
	}
	return s.hub.RerunWorkflow(id)
}

// BranchDiff returns a branch's changes against its base, uncommitted
// ones included.
func (s *HiveService) BranchDiff(ctx context.Context, ref string) (core.BranchDiff, error) {
	if err := guard(ctx); err != nil {
		return core.BranchDiff{}, err
	}
	return s.hub.BranchDiff(ref)
}

// CommentBranch adds a review comment, on a file and line when given;
// send sends the comments not sent yet to the branch's agent.
func (s *HiveService) CommentBranch(ctx context.Context, ref, path string, line int, body string, send bool) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.CommentBranch(ref, path, line, body, send)
}

// SendComments sends a branch's comments not sent yet to its agent.
func (s *HiveService) SendComments(ctx context.Context, ref string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.SendComments(ref)
}

// ReadyBranch runs a branch's gates and says whether it is ready.
func (s *HiveService) ReadyBranch(ctx context.Context, ref string) (bool, error) {
	if err := guard(ctx); err != nil {
		return false, err
	}
	return s.hub.ReadyBranch(ref)
}

// PushBranch pushes a branch to its remote, because the person asked.
func (s *HiveService) PushBranch(ctx context.Context, ref string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.PushBranch(ref)
}

// OpenPullRequest opens a pull request for a branch and returns its URL.
func (s *HiveService) OpenPullRequest(ctx context.Context, ref string, draft, push bool) (string, error) {
	if err := guard(ctx); err != nil {
		return "", err
	}
	return s.hub.OpenPullRequest(ref, draft, push)
}

// CleanBranch removes a branch's worktree (force: also with uncommitted
// changes, saved first).
func (s *HiveService) CleanBranch(ctx context.Context, ref string, force bool) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.CleanBranch(ref, force)
}

// CreateEnvironment makes an environment for a directory on a machine
// ("" for this computer).
func (s *HiveService) CreateEnvironment(ctx context.Context, machine, id, dir string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.CreateEnvironment(machine, id, dir)
}

// Machines are the saved machines and how the app is connected to them.
func (s *HiveService) Machines(ctx context.Context) []core.MachineStatus {
	if guard(ctx) != nil {
		return []core.MachineStatus{}
	}
	return s.hub.Machines()
}

// RefreshMachines reads the saved machines again.
func (s *HiveService) RefreshMachines(ctx context.Context) {
	if guard(ctx) != nil {
		return
	}
	s.hub.RefreshMachines()
}

// Discover looks for Hive runtimes on the local network.
func (s *HiveService) Discover(ctx context.Context) (core.Discovery, error) {
	if err := guard(ctx); err != nil {
		return core.Discovery{}, err
	}
	return s.hub.Discover()
}

// CheckPair proves the identity of a runtime or host before pairing,
// saving nothing.
func (s *HiveService) CheckPair(ctx context.Context, req core.PairRequest) (core.IdentityCheck, error) {
	if err := guard(ctx); err != nil {
		return core.IdentityCheck{}, err
	}
	return s.hub.CheckPair(req)
}

// Pair saves a machine if it proves the identity the user confirmed.
func (s *HiveService) Pair(ctx context.Context, req core.PairRequest, expectID string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.Pair(req, expectID)
}

// CheckTrust proves a saved machine's identity, saving nothing.
func (s *HiveService) CheckTrust(ctx context.Context, name string) (core.IdentityCheck, error) {
	if err := guard(ctx); err != nil {
		return core.IdentityCheck{}, err
	}
	return s.hub.CheckTrust(name)
}

// Trust pins the identity the user confirmed for a saved machine.
func (s *HiveService) Trust(ctx context.Context, name, expectID string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.Trust(name, expectID)
}

// MachineAction enables, disables, revokes or removes a saved machine.
func (s *HiveService) MachineAction(ctx context.Context, name, action string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	return s.hub.MachineAction(name, action)
}

// OpenLink opens a link from a terminal in the system browser. Only web
// and mail links are opened: a terminal must not launch other handlers.
func (s *HiveService) OpenLink(ctx context.Context, link string) error {
	if err := guard(ctx); err != nil {
		return err
	}
	u, err := url.Parse(link)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "http", "https", "mailto":
		return s.shell.openURL(u.String())
	}
	return errors.New("only http, https and mailto links are opened")
}

// observerWindowPrefix names observer windows ("observe-<credential>").
const observerWindowPrefix = "observe-"

// errObserverWindow refuses control from an observer window: it may only
// look (ObserverService).
var errObserverWindow = errors.New("an observer window cannot control Hive")

// guard refuses calls from observer windows. Calls with no window (tests,
// the server build's browser) are the main window's.
func guard(ctx context.Context) error {
	if w, ok := ctx.Value(application.WindowKey).(application.Window); ok && strings.HasPrefix(w.Name(), observerWindowPrefix) {
		return errObserverWindow
	}
	return nil
}

// OpenObserver opens a read-only window for shared screens: it gets a
// scoped, expiring observer credential of its own (revoked when it
// closes) and has no way to type or act.
func (s *HiveService) OpenObserver(ctx context.Context, req core.ObserveRequest) (string, error) {
	if err := guard(ctx); err != nil {
		return "", err
	}
	return s.shell.openObserver(req)
}
