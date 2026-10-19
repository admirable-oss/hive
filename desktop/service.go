package main

import (
	"errors"
	"net/url"

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
	startAtLogin() (bool, error)
	setStartAtLogin(on bool) error
}

// Connection is how the app is connected to the daemon.
func (s *HiveService) Connection() core.Connection { return s.hub.Connection() }

// Workspace is the daemon's environments, tabs, panes and agents.
func (s *HiveService) Workspace() core.Workspace { return s.hub.Workspace() }

// Settings are the parts of the Hive configuration the app follows.
func (s *HiveService) Settings() core.Settings { return s.hub.Settings() }

// Retry connects again now.
func (s *HiveService) Retry() { s.hub.Retry() }

// OpenView streams a pane's screen at cols × rows to viewer (a window's
// own ID).
func (s *HiveService) OpenView(viewer, paneID string, cols, rows int) error {
	return s.hub.OpenView(viewer, paneID, cols, rows)
}

// CloseView says viewer stopped showing a pane.
func (s *HiveService) CloseView(viewer, paneID string) { s.hub.CloseView(viewer, paneID) }

// RefreshView sends a pane's whole screen again.
func (s *HiveService) RefreshView(paneID string) { s.hub.Refresh(paneID) }

// Input types bytes into a pane.
func (s *HiveService) Input(paneID, data string) error { return s.hub.Input(paneID, data) }

// Paste pastes text into a pane (bracketed when the program asks).
func (s *HiveService) Paste(paneID, text string) error { return s.hub.Paste(paneID, text) }

// PasteClipboard pastes the clipboard into a pane.
func (s *HiveService) PasteClipboard(paneID string) error {
	return s.hub.Paste(paneID, s.shell.clipboard())
}

// Copy puts text on the clipboard.
func (s *HiveService) Copy(text string) { s.shell.setClipboard(text) }

// ResizeTab sets a tab's area in cells.
func (s *HiveService) ResizeTab(tabID string, cols, rows int) error {
	return s.hub.ResizeTab(tabID, cols, rows)
}

// FocusTab makes a tab active.
func (s *HiveService) FocusTab(tabID string) error { return s.hub.FocusTab(tabID) }

// NewTab opens a shell in a new tab and returns its pane.
func (s *HiveService) NewTab(envID string) (string, error) { return s.hub.NewTab(envID) }

// CloseTab closes a tab and stops its processes.
func (s *HiveService) CloseTab(tabID string) error { return s.hub.CloseTab(tabID) }

// FocusPane focuses a pane.
func (s *HiveService) FocusPane(paneID string) error { return s.hub.FocusPane(paneID) }

// SplitPane splits a pane (left, right, up or down) and returns the new one.
func (s *HiveService) SplitPane(paneID, direction string) (string, error) {
	return s.hub.SplitPane(paneID, direction)
}

// ResizePane moves a pane's border.
func (s *HiveService) ResizePane(paneID, direction string, cells int) error {
	return s.hub.ResizePane(paneID, direction, cells)
}

// ZoomPane toggles a pane filling its tab.
func (s *HiveService) ZoomPane(paneID string) error { return s.hub.ZoomPane(paneID) }

// ClosePane closes a pane and stops its process.
func (s *HiveService) ClosePane(paneID string) error { return s.hub.ClosePane(paneID) }

// RenamePane renames a pane.
func (s *HiveService) RenamePane(paneID, name string) error { return s.hub.RenamePane(paneID, name) }

// PopOut opens a pane in a window of its own.
func (s *HiveService) PopOut(paneID string) error { return s.shell.popOut(paneID) }

// History returns a pane's scrollback and screen as text (its last lines).
func (s *HiveService) History(paneID string, lines int) ([]string, error) {
	return s.hub.History(paneID, lines)
}

// StartAtLogin says whether the app starts when you log in.
func (s *HiveService) StartAtLogin() (bool, error) { return s.shell.startAtLogin() }

// SetStartAtLogin makes the app start when you log in, or not.
func (s *HiveService) SetStartAtLogin(on bool) error { return s.shell.setStartAtLogin(on) }

// Manifests lists the agents a machine's Hive can start ("" for this
// computer).
func (s *HiveService) Manifests(machine string) ([]core.Manifest, error) {
	return s.hub.Manifests(machine)
}

// StartAgent starts an agent and returns its pane.
func (s *HiveService) StartAgent(req core.StartAgentRequest) (string, error) {
	return s.hub.StartAgent(req)
}

// Prompt sends a prompt to an agent and returns how it reacted.
func (s *HiveService) Prompt(agentID, text string) (string, error) {
	return s.hub.Prompt(agentID, text)
}

// StopAgent stops an agent.
func (s *HiveService) StopAgent(agentID string) error { return s.hub.StopAgent(agentID) }

// CreateEnvironment makes an environment for a directory on a machine
// ("" for this computer).
func (s *HiveService) CreateEnvironment(machine, id, dir string) error {
	return s.hub.CreateEnvironment(machine, id, dir)
}

// Machines are the saved machines and how the app is connected to them.
func (s *HiveService) Machines() []core.MachineStatus { return s.hub.Machines() }

// RefreshMachines reads the saved machines again.
func (s *HiveService) RefreshMachines() { s.hub.RefreshMachines() }

// Discover looks for Hive runtimes on the local network.
func (s *HiveService) Discover() (core.Discovery, error) { return s.hub.Discover() }

// CheckPair proves the identity of a runtime or host before pairing,
// saving nothing.
func (s *HiveService) CheckPair(req core.PairRequest) (core.IdentityCheck, error) {
	return s.hub.CheckPair(req)
}

// Pair saves a machine if it proves the identity the user confirmed.
func (s *HiveService) Pair(req core.PairRequest, expectID string) error {
	return s.hub.Pair(req, expectID)
}

// CheckTrust proves a saved machine's identity, saving nothing.
func (s *HiveService) CheckTrust(name string) (core.IdentityCheck, error) {
	return s.hub.CheckTrust(name)
}

// Trust pins the identity the user confirmed for a saved machine.
func (s *HiveService) Trust(name, expectID string) error { return s.hub.Trust(name, expectID) }

// MachineAction enables, disables, revokes or removes a saved machine.
func (s *HiveService) MachineAction(name, action string) error {
	return s.hub.MachineAction(name, action)
}

// OpenLink opens a link from a terminal in the system browser. Only web
// and mail links are opened: a terminal must not launch other handlers.
func (s *HiveService) OpenLink(link string) error {
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
