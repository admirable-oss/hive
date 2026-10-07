package process

import "context"

type Command struct {
	Path       string
	Args       []string
	WorkingDir string
	Env        []string
	StdoutPath string
	StderrPath string
}

type Handle interface {
	PID() int
	Wait() error
	Kill() error
}

type Runner interface {
	Start(ctx context.Context, cmd Command) (Handle, error)
}
