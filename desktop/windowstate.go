package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// windowState is where the main window was, kept in the user's config
// directory (Hive/desktop.json), so the app opens where it was left. The
// same file keeps the app's own preferences: UI state, never screens.
type windowState struct {
	X, Y, Width, Height int
	// UpdateChecks off stops the daily update check; unset means on.
	UpdateChecks *bool `json:"update_checks,omitempty"`
}

func windowStatePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Hive", "desktop.json")
}

// readState reads the whole file (zero when there is none).
func readState() windowState {
	var w windowState
	if path := windowStatePath(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &w)
		}
	}
	return w
}

func writeState(w windowState) error {
	path := windowStatePath()
	if path == "" {
		return errors.New("no configuration directory")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(w)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// loadWindowState reads the saved bounds; ok is false when there are none
// that make sense.
func loadWindowState() (windowState, bool) {
	w := readState()
	if w.Width < 560 || w.Height < 360 || w.Width > 20000 || w.Height > 20000 {
		return windowState{}, false
	}
	return w, true
}

// saveWindowState keeps the main window's bounds, and the preferences.
func saveWindowState(w windowState) {
	if w.Width < 560 || w.Height < 360 {
		return
	}
	cur := readState()
	cur.X, cur.Y, cur.Width, cur.Height = w.X, w.Y, w.Width, w.Height
	_ = writeState(cur)
}

// updateChecks says whether the app checks for updates by itself.
func updateChecks() bool {
	w := readState()
	return w.UpdateChecks == nil || *w.UpdateChecks
}

func saveUpdateChecks(on bool) error {
	w := readState()
	w.UpdateChecks = &on
	return writeState(w)
}
