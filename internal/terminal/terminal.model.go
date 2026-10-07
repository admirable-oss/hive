package terminal

// Size represents terminal dimensions.
type Size struct {
	Width  uint16 `json:"width"`
	Height uint16 `json:"height"`
}

// Command describes what to launch inside a PTY.
type Command struct {
	Path       string   `json:"path"`
	Args       []string `json:"args"`
	WorkingDir string   `json:"working_dir"`
	Env        []string `json:"env"`
	StdoutPath string   `json:"stdout_path"`
	StderrPath string   `json:"stderr_path"`
	Size       Size     `json:"size"`
}

// DefaultSize is used when no explicit size is provided.
var DefaultSize = Size{Width: 220, Height: 50}
