package main

import (
	"context"
	"errors"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/admirable-oss/hive/desktop/internal/core"
)

// observers are the open observer windows, by window name.
type observers struct {
	mu  sync.Mutex
	all map[string]*core.Observation
}

// openObserver makes a credential for a new observer window, opens the
// window and follows the daemon for it. The credential is revoked when
// the window closes.
func (d *desktop) openObserver(req core.ObserveRequest) (string, error) {
	obs, err := d.hub.Local().CreateObserver(req)
	if err != nil {
		return "", err
	}
	name := observerWindowPrefix + obs.ID
	win := d.newWindow(name, "Hive · observing "+obs.Scope, "/#/observe", 1100, 720)
	o := core.Observe(obs, func(event string, data any) { win.EmitEvent(event, data) }, d.log)
	d.obs.mu.Lock()
	d.obs.all[name] = o
	d.obs.mu.Unlock()
	win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		d.obs.mu.Lock()
		delete(d.obs.all, name)
		d.obs.mu.Unlock()
		go func() {
			o.Close()
			if err := d.hub.Local().RevokeObserver(obs.ID); err != nil {
				d.log.Warn("could not revoke an observer window's credential", "id", obs.ID, "err", err)
			}
		}()
	})
	win.Show()
	return name, nil
}

// closeObservers revokes every observer window's credential (on quit).
func (d *desktop) closeObservers() {
	d.obs.mu.Lock()
	all := d.obs.all
	d.obs.all = map[string]*core.Observation{}
	d.obs.mu.Unlock()
	for name, o := range all {
		o.Close()
		_ = d.hub.Local().RevokeObserver(name[len(observerWindowPrefix):])
	}
}

// ObserverService is all an observer window can call: it reads its own
// observation, and chooses which screens to watch. Calls from any other
// window are refused.
type ObserverService struct{ d *desktop }

var errNotObserver = errors.New("only an observer window observes")

func (s *ObserverService) observation(ctx context.Context) (*core.Observation, error) {
	w, ok := ctx.Value(application.WindowKey).(application.Window)
	if !ok {
		return nil, errNotObserver
	}
	s.d.obs.mu.Lock()
	defer s.d.obs.mu.Unlock()
	o, ok := s.d.obs.all[w.Name()]
	if !ok {
		return nil, errNotObserver
	}
	return o, nil
}

// View is what the calling observer window shows.
func (s *ObserverService) View(ctx context.Context) (core.ObservedView, error) {
	o, err := s.observation(ctx)
	if err != nil {
		return core.ObservedView{}, err
	}
	return o.View(), nil
}

// Watch streams a pane's screen to the calling window, as text.
func (s *ObserverService) Watch(ctx context.Context, pane string) error {
	o, err := s.observation(ctx)
	if err != nil {
		return err
	}
	return o.Watch(pane)
}

// Unwatch stops streaming a pane to the calling window.
func (s *ObserverService) Unwatch(ctx context.Context, pane string) error {
	o, err := s.observation(ctx)
	if err != nil {
		return err
	}
	o.Unwatch(pane)
	return nil
}
