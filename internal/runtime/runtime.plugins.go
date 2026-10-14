package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/metadata"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/plugin"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/session"
)

// pluginDir holds installed plugins: shared by every session, beside them.
func (c Config) pluginDir() string {
	if c.Home != "" {
		return filepath.Join(c.Home, "plugins")
	}
	return filepath.Join(c.root(), "plugins")
}

// newPluginHost builds the host that runs the enabled plugins' commands.
func newPluginHost(cfg Config, log *slog.Logger) *plugin.Host {
	env := cfg.launchEnv()
	return plugin.NewHost(plugin.HostConfig{
		Store:   plugin.NewStore(cfg.pluginDir()),
		Version: buildinfo.Get().Version,
		Session: session.Normalize(cfg.Session),
		Env: func(vars map[string]string) []string {
			if env == nil {
				out := os.Environ()
				for k, v := range vars {
					out = append(out, k+"="+v)
				}
				return out
			}
			return env.Build(vars)
		},
		LogDir: filepath.Join(cfg.root(), "logs", "plugins"),
		Logger: log,
	})
}

// forwardEvents passes every event a plugin handles except the loss marker;
// plugins can re-read the workspace after their next event.
func forwardEvents(ctx context.Context, bus *event.Bus, host *plugin.Host) {
	sub := bus.Subscribe(event.DefaultBuffer)
	defer sub.Close()
	for {
		ev, err := sub.Next(ctx)
		if err != nil {
			return
		}
		if ev.Type == event.Lost {
			continue // plugins can re-read state after their next meaningful event
		}
		host.Event(ev.Type, ev.Data) //nolint:contextcheck // commands run in the host's lifetime, not this loop's
	}
}

// PluginRunRequest runs a plugin's action (plugin.run).
type PluginRunRequest struct {
	Plugin string `json:"plugin"`
	Action string `json:"action"`
	// Environment, Tab and Pane say where the user is, for the action's
	// context.
	Environment string `json:"environment,omitempty"`
	Tab         string `json:"tab,omitempty"`
	Pane        string `json:"pane,omitempty"`
}

func registerPlugins(r *protocol.Router, host *plugin.Host) {
	r.MustRegister("plugin.list", protocol.Method(func(context.Context, struct{}) ([]plugin.Status, error) {
		return host.Status(), nil
	}))
	r.MustRegister("plugin.reload", protocol.Method(func(context.Context, struct{}) ([]plugin.Status, error) {
		return host.Reload(), nil //nolint:contextcheck // plugins outlive the request that reloads them
	}))
	r.MustRegister("plugin.run", protocol.Method(func(ctx context.Context, p PluginRunRequest) (plugin.Result, error) {
		res, err := host.Action(ctx, p.Plugin, p.Action, plugin.Context{Environment: p.Environment, Tab: p.Tab, Pane: p.Pane})
		if errors.Is(err, plugin.ErrNotFound) || errors.Is(err, plugin.ErrNoAction) {
			err = protocol.NewError(protocol.ErrorCodeNotFound, err)
		}
		return res, err
	}))
}

// metadataTargets checks that what metadata is reported on exists.
type metadataTargets struct {
	panes *pane.Service
	envs  environment.Service
}

func registerMetadata(r *protocol.Router, store *metadata.Store, targets metadataTargets, bus *event.Bus) {
	r.MustRegister("metadata.report", protocol.Method(func(ctx context.Context, rep metadata.Report) (metadata.Entry, error) {
		switch {
		case rep.Pane != "":
			if _, err := targets.panes.Pane(ctx, rep.Pane); err != nil {
				return metadata.Entry{}, protocol.NewError(protocol.ErrorCodeNotFound, err)
			}
		case rep.Environment != "":
			if _, err := targets.envs.Get(ctx, rep.Environment); err != nil {
				return metadata.Entry{}, protocol.NewError(protocol.ErrorCodeNotFound, err)
			}
		}
		e, err := store.Report(rep)
		if err != nil {
			return e, protocol.NewError(protocol.ErrorCodeInvalidParams, err)
		}
		bus.Publish(metadata.EventChange, e)
		return e, nil
	}))
	r.MustRegister("metadata.list", protocol.Method(func(context.Context, struct{}) ([]metadata.Entry, error) {
		return store.List(), nil
	}))
}

// expireMetadata announces metadata that ran out, and drops the metadata
// of panes and environments that went.
func expireMetadata(ctx context.Context, store *metadata.Store, bus *event.Bus, log *slog.Logger) {
	sub := bus.Subscribe(event.DefaultBuffer, pane.EventPaneClosed, event.EnvironmentRemoved)
	defer sub.Close()
	events := make(chan event.Event)
	go func() {
		defer close(events)
		for {
			ev, err := sub.Next(ctx)
			if err != nil {
				return
			}
			select {
			case events <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			for range events { // drain until the reader stops
			}
			return
		case <-t.C:
			for _, e := range store.Expire() {
				bus.Publish(metadata.EventChange, e)
			}
		case ev, ok := <-events:
			if !ok {
				return
			}
			var d struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(ev.Data, &d); err != nil || d.ID == "" {
				log.Debug("undecodable event", "type", ev.Type, "err", err)
				continue
			}
			target := metadata.Target{Pane: d.ID}
			if ev.Type == event.EnvironmentRemoved {
				target = metadata.Target{Environment: d.ID}
			}
			store.Forget(target)
		}
	}
}
