package runtime

import (
	"context"
	"errors"
	"strings"

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

// salvage rebuilds the record of agent id, in envID, from its shim's spec
// and state when its process.json cannot be read. The record is left
// running for recovery to adopt (or to record how it ended).
func (r shimRunner) salvage(envID, id string) (process.Process, bool) {
	spec, st, err := r.launcher.Describe(id)
	if err != nil || spec.ID != id {
		return process.Process{}, false
	}
	p := process.Process{
		ID: id, EnvironmentID: envID, Command: spec.Path, Args: spec.Args, WorkingDir: spec.WorkingDir,
		Terminal: spec.Terminal, PID: st.PID, Status: process.StatusRunning, StartedAt: st.StartedAt,
	}
	// Only Hive's own variables: the spec holds the whole environment.
	for _, kv := range spec.Env {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "HIVE_PANE_ID", "HIVE_TAB_ID":
			if p.Env == nil {
				p.Env = map[string]string{}
			}
			p.Env[k] = v
		}
	}
	p.ForPane = p.Env["HIVE_PANE_ID"]
	return p, true
}
