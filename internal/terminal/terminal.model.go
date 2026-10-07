package terminal

// Size represents terminal dimensions.
type Size struct {
	Width  uint16 `json:"width"`
	Height uint16 `json:"height"`
}

// Command describes what to launch inside a PTY. A PTY merges stdout and
// stderr, so there is a single log file.
type Command struct {
	Path       string
	Args       []string
	WorkingDir string
	LogPath    string // optional: every byte of output is appended here
	Size       Size   // zero means DefaultSize
}

// DefaultSize is used when no explicit size is provided.
var DefaultSize = Size{Width: 220, Height: 50}
