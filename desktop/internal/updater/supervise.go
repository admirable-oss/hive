package updater

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Flag starts the binary as the update supervisor: `<app> --apply-update
// <plan file>`, before anything of the app itself starts.
const Flag = "--apply-update"

// TokenEnv carries the supervisor's token to the new app, which writes it
// to the health file once it has started (ReportStarted).
const TokenEnv = "HIVE_DESKTOP_UPDATE_TOKEN" //nolint:gosec // G101: a variable's name, not a credential

// Plan is an update ready to apply: what to replace, with what, and who is
// waiting for it.
type Plan struct {
	Target    Target `json:"target"`
	Staged    string `json:"staged"`
	From      string `json:"from"`
	To        string `json:"to"`
	ParentPID int    `json:"parent_pid"`
	Token     string `json:"token"`
	// StateDir holds the health file and the result.
	StateDir string `json:"state_dir"`
}

// Result is how the last update went, for the app to tell on its next start.
type Result struct {
	OK     bool   `json:"ok"`
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason,omitempty"`
}

func healthFile(dir string) string { return filepath.Join(dir, "update-health") }
func resultFile(dir string) string { return filepath.Join(dir, "update-result.json") }

// NewPlan writes a plan for staged and returns the plan file.
func NewPlan(t Target, staged, from, to, stateDir string) (string, error) {
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		return "", err
	}
	p := Plan{Target: t, Staged: staged, From: from, To: to, ParentPID: os.Getpid(), Token: hex.EncodeToString(tok), StateDir: stateDir}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", err
	}
	_ = os.Remove(healthFile(stateDir))
	data, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	path := filepath.Join(stateDir, "update-plan.json")
	return path, os.WriteFile(path, data, 0o600)
}

// StartSupervisor starts exe as the supervisor of the plan, detached, so
// it outlives the app, which quits next.
func StartSupervisor(exe, planFile string) error {
	cmd := exec.Command(exe, Flag, planFile) //nolint:noctx // the supervisor outlives this process
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Options tune Supervise; zero values are the defaults.
type Options struct {
	// Health is how long the new app has to report that it started (60s).
	Health time.Duration
	// ParentExit is how long to wait for the old app to quit (60s).
	ParentExit time.Duration
	// Poll is how often to look (200ms).
	Poll time.Duration
}

// Supervise applies a plan: it waits for the app to quit, swaps the new
// version in, starts it and waits for it to report that it started. If it
// does not, it kills it, puts the previous version back and starts that.
// Either way it records a Result for the app's next start.
func Supervise(planFile string, opts Options) (Result, error) {
	opts.Health = cmpDur(opts.Health, 60*time.Second)
	opts.ParentExit = cmpDur(opts.ParentExit, 60*time.Second)
	opts.Poll = cmpDur(opts.Poll, 200*time.Millisecond)

	data, err := os.ReadFile(planFile)
	if err != nil {
		return Result{}, err
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return Result{}, fmt.Errorf("read the update plan: %w", err)
	}
	_ = os.Remove(planFile)
	res := Result{From: p.From, To: p.To}
	finish := func(r Result) (Result, error) {
		out, _ := json.Marshal(r)
		_ = os.WriteFile(resultFile(p.StateDir), out, 0o600)
		if !r.OK {
			return r, errors.New(r.Reason)
		}
		return r, nil
	}

	waitExit(p.ParentPID, opts.ParentExit, opts.Poll)
	if err := swap(p.Target, p.Staged); err != nil {
		res.Reason = "could not install " + p.To + ": " + err.Error()
		_, _ = launch(p.Target.Exe, "")
		return finish(res)
	}
	child, err := launch(p.Target.Exe, p.Token)
	if err == nil && waitHealthy(p, child, opts) {
		res.OK = true
		return finish(res)
	}
	if child != nil {
		_ = child.Process.Kill()
	}
	res.Reason = p.To + " did not start, so " + p.From + " was put back"
	if err := restore(p.Target); err != nil {
		res.Reason += ", but putting it back failed: " + err.Error() + " (the previous version is at " + Previous(p.Target) + ")"
		return finish(res)
	}
	_, _ = launch(p.Target.Exe, "")
	return finish(res)
}

// waitHealthy waits for the new app to write the token to the health
// file, and gives up when it exits or the time is up.
func waitHealthy(p Plan, child *exec.Cmd, opts Options) bool {
	exited := make(chan struct{})
	go func() {
		_ = child.Wait()
		close(exited)
	}()
	deadline := time.After(opts.Health)
	tick := time.NewTicker(opts.Poll)
	defer tick.Stop()
	for {
		if b, err := os.ReadFile(healthFile(p.StateDir)); err == nil && strings.TrimSpace(string(b)) == p.Token {
			_ = os.Remove(healthFile(p.StateDir))
			return true
		}
		select {
		case <-exited:
			// One last look: it may have reported and then quit.
			b, err := os.ReadFile(healthFile(p.StateDir))
			return err == nil && strings.TrimSpace(string(b)) == p.Token
		case <-deadline:
			return false
		case <-tick.C:
		}
	}
}

// launch starts exe detached; with a token, the new app reports to it.
func launch(exe, token string) (*exec.Cmd, error) {
	cmd := exec.Command(exe) //nolint:noctx // the app outlives the supervisor
	cmd.Env = os.Environ()
	if token != "" {
		cmd.Env = append(cmd.Env, TokenEnv+"="+token)
	}
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// ReportStarted is called by the app once it has started: when an update
// supervisor launched it, it writes the supervisor's token to the health
// file. The token is removed from the environment, so nothing the app
// starts inherits it.
func ReportStarted(stateDir string) {
	tok := os.Getenv(TokenEnv)
	if tok == "" {
		return
	}
	_ = os.Unsetenv(TokenEnv)
	_ = os.WriteFile(healthFile(stateDir), []byte(tok+"\n"), 0o600) //nolint:gosec // G703: the app's own state directory
}

// TakeResult returns how the last update went, once: the file is removed.
func TakeResult(stateDir string) (Result, bool) {
	data, err := os.ReadFile(resultFile(stateDir))
	if err != nil {
		return Result{}, false
	}
	_ = os.Remove(resultFile(stateDir))
	var r Result
	return r, json.Unmarshal(data, &r) == nil
}

func cmpDur(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}
