package tui

// Test-only access to unexported helpers.
var KeyToBytes = keyToBytes

// PollTick is the message the dashboard's refresh timer delivers.
func PollTick() any { return pollTickMsg{} }
