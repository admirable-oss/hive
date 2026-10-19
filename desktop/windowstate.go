package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// windowState is where the main window was, kept in the user's config
// directory (Hive/desktop.json), so the app opens where it was left.
type windowState struct {
	X, Y, Width, Height int
}

func windowStatePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Hive", "desktop.json")
}

// loadWindowState reads the saved bounds; ok is false when there are none
// that make sense.
func loadWindowState() (windowState, bool) {
	path := windowStatePath()
	if path == "" {
		return windowState{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return windowState{}, false
	}
	var w windowState
	if json.Unmarshal(data, &w) != nil || w.Width < 560 || w.Height < 360 || w.Width > 20000 || w.Height > 20000 {
		return windowState{}, false
	}
	return w, true
}

func saveWindowState(w windowState) {
	path := windowStatePath()
	if path == "" || w.Width < 560 || w.Height < 360 {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, _ := json.Marshal(w)
	_ = os.WriteFile(path, data, 0o600)
}
