package runtime

import (
	"context"
	"errors"

	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/shim"
)

// shimRunner runs plain processes under shims, for package process: it is
// both its Runner and its Adopter.
type shimRunner struct {
	launcher *shim.Launcher
}

func (r shimRunner) Start(ctx context.Context, cmd process.Command) (process.Handle, error) {
	return r.launcher.StartPlain(ctx, shim.Spec{
		ID: cmd.ID, Path: cmd.Path, Args: cmd.Args, WorkingDir: cmd.WorkingDir,
		StdoutPath: cmd.StdoutPath, StderrPath: cmd.StderrPath,
	})
}

func (r shimRunner) Adopt(ctx context.Context, p process.Process) (process.Handle, error) {
	sess, err := r.launcher.Adopt(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	remote, ok := sess.(*shim.Remote)
	if !ok {
		return nil, errors.New("runtime: shim launcher returned an unexpected session")
	}
	return remote, nil
}
