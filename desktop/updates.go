package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/admirable-oss/hive/desktop/internal/updater"
)

// Set by release builds:
//
//	-ldflags "-X main.version=1.1.0 -X main.releaseKey=<base64 ed25519 public key>"
//
// A build without them (development) reports "dev" and never updates
// itself: it cannot verify a release.
var (
	version    = "dev"
	releaseKey = ""
)

// eventUpdate carries the update status to the frontend.
const eventUpdate = "hive:update"

// checkEvery is how often the app looks for a newer release, when allowed.
const checkEvery = 24 * time.Hour

// UpdateStatus is what the app knows about its own updates.
type UpdateStatus struct {
	// Version is the running app's.
	Version string `json:"version"`
	// Supported: this build and installation can update themselves.
	// Reason says why not.
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
	// Automatic: the app checks for updates by itself (once a day). The
	// check is its only request to the network, and can be turned off.
	Automatic  bool `json:"automatic"`
	Checking   bool `json:"checking"`
	Installing bool `json:"installing"`
	// Available is a newer release with a package for this system.
	Available   *updater.Release `json:"available,omitempty"`
	LastChecked time.Time        `json:"last_checked,omitzero"`
	Error       string           `json:"error,omitempty"`
	// Result is how the update before this start went, shown once.
	Result *updater.Result `json:"result,omitempty"`
}

// updates is the app's side of updating itself.
type updates struct {
	d      *desktop
	state  string // where downloads, plans and results live
	target updater.Target
	stop   chan struct{}

	mu sync.Mutex
	st UpdateStatus
}

func newUpdates(d *desktop) *updates {
	u := &updates{d: d, stop: make(chan struct{}), st: UpdateStatus{Version: version, Automatic: updateChecks()}}
	if dir, err := os.UserConfigDir(); err == nil {
		u.state = filepath.Join(dir, "Hive", "updates")
	}
	var err error
	switch {
	case serverBuild:
		err = errors.New("the server build does not update itself")
	case releaseKey == "":
		err = updater.ErrNoKey
	case u.state == "":
		err = errors.New("no configuration directory to keep updates in")
	default:
		var exe string
		if exe, err = os.Executable(); err == nil {
			if exe, err = filepath.EvalSymlinks(exe); err == nil {
				u.target, err = updater.Locate(runtime.GOOS, exe, os.Getenv)
			}
		}
	}
	u.st.Supported = err == nil
	if err != nil {
		u.st.Reason = err.Error()
	}
	return u
}

// started runs once the app is up: it tells a waiting update supervisor
// so, picks up how the last update went, and starts the daily check.
func (u *updates) started() {
	if u.state == "" {
		return
	}
	updater.ReportStarted(u.state)
	if r, ok := updater.TakeResult(u.state); ok {
		u.set(func(st *UpdateStatus) { st.Result = &r })
	}
	if u.status().Supported {
		go u.loop()
	}
}

func (u *updates) close() {
	select {
	case <-u.stop:
	default:
		close(u.stop)
	}
}

// loop checks a little after start, then once a day, while allowed.
func (u *updates) loop() {
	wait := time.NewTimer(30 * time.Second)
	defer wait.Stop()
	for {
		select {
		case <-u.stop:
			return
		case <-wait.C:
		}
		if u.status().Automatic {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, _ = u.check(ctx)
			cancel()
		}
		wait.Reset(checkEvery)
	}
}

func (u *updates) status() UpdateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.st
}

func (u *updates) set(change func(*UpdateStatus)) {
	u.mu.Lock()
	change(&u.st)
	st := u.st
	u.mu.Unlock()
	if u.d.app != nil {
		u.d.app.Event.Emit(eventUpdate, st)
	}
}

func (u *updates) config() updater.Config {
	key, _ := updater.ParseKey(releaseKey)
	return updater.Config{
		Version: version, PublicKey: key, Channel: u.d.hub.Settings().UpdateChannel,
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	}
}

// check looks for a newer release now.
func (u *updates) check(ctx context.Context) (UpdateStatus, error) {
	if st := u.status(); !st.Supported {
		return st, errors.New(st.Reason)
	}
	u.set(func(st *UpdateStatus) { st.Checking, st.Error = true, "" })
	rel, err := u.config().Check(ctx)
	u.set(func(st *UpdateStatus) {
		st.Checking, st.LastChecked = false, time.Now()
		st.Available = nil
		if err != nil {
			st.Error = err.Error()
			return
		}
		if rel.Newer && rel.Asset != "" {
			st.Available = &rel
		}
	})
	return u.status(), err
}

// install downloads, verifies and stages the available release, starts
// the supervisor that swaps it in, and quits; agents keep running.
func (u *updates) install(ctx context.Context) error {
	st := u.status()
	switch {
	case !st.Supported:
		return errors.New(st.Reason)
	case st.Installing:
		return errors.New("an update is already being installed")
	case st.Available == nil:
		return errors.New("no update is available; check for one first")
	}
	rel := *st.Available
	u.set(func(st *UpdateStatus) { st.Installing, st.Error = true, "" })
	err := u.prepare(ctx, rel)
	if err != nil {
		u.set(func(st *UpdateStatus) { st.Installing, st.Error = false, err.Error() })
		return err
	}
	u.d.log.Info("installing update", "from", version, "to", rel.Version)
	u.d.app.Quit()
	return nil
}

func (u *updates) prepare(ctx context.Context, rel updater.Release) error {
	pkg, err := u.config().Download(ctx, rel, filepath.Join(u.state, "downloads"))
	if err != nil {
		return err
	}
	staged, err := updater.Stage(u.target, pkg)
	if err != nil {
		return err
	}
	plan, err := updater.NewPlan(u.target, staged, version, rel.Version, u.state)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return updater.StartSupervisor(exe, plan)
}

// setAutomatic turns the daily check on or off, remembered across starts.
func (u *updates) setAutomatic(on bool) error {
	if err := saveUpdateChecks(on); err != nil {
		return err
	}
	u.set(func(st *UpdateStatus) { st.Automatic = on })
	return nil
}

// superviseUpdate runs the binary as an update supervisor, when started
// with updater.Flag, and reports whether it did.
func superviseUpdate(args []string) bool {
	if len(args) != 3 || args[1] != updater.Flag {
		return false
	}
	if _, err := updater.Supervise(args[2], updater.Options{}); err != nil {
		_, _ = os.Stderr.WriteString("hive-desktop: update: " + err.Error() + "\n")
	}
	return true
}
