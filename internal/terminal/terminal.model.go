package terminal

// Size represents terminal dimensions.
type Size struct {
	Width  uint16 `json:"width"`
	Height uint16 `json:"height"`
}

// Valid reports whether both dimensions are set.
func (s Size) Valid() bool { return s.Width > 0 && s.Height > 0 }

// Or fills each unset dimension of s from the first fallback that has it,
// so a caller can set only a height.
func (s Size) Or(fallbacks ...Size) Size {
	for _, f := range fallbacks {
		if s.Width == 0 {
			s.Width = f.Width
		}
		if s.Height == 0 {
			s.Height = f.Height
		}
	}
	return s
}

// Command describes what to launch inside a PTY. A PTY merges stdout and
// stderr, so there is a single log file.
type Command struct {
	// ID names the session; factories whose sessions outlive the daemon
	// (shims) use it to find the session again.
	ID         string
	Path       string
	Args       []string
	WorkingDir string
	Env        []string // nil inherits the daemon's environment
	LogPath    string   // optional: every byte of output is appended here
	Size       Size     // zero means the factory's default
}

// DefaultSize is used when no explicit size is provided.
var DefaultSize = Size{Width: 220, Height: 50}
