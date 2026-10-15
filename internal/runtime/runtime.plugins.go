package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/metadata"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/plugin"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/session"
	"github.com/admirable-oss/hive/internal/vt"
)

// pluginDir holds installed plugins: shared by every session, beside them.
func (c Config) pluginDir() string {
	if c.Home != "" {
		return filepath.Join(c.Home, "plugins")
	}
	return filepath.Join(c.root(), "plugins")
}

// newPluginHost builds the host that runs the enabled plugins: each on its
// own socket, scoped to its capabilities, with plugin events on the bus.
func newPluginHost(cfg Config, log *slog.Logger, router *protocol.Router, bus *event.Bus) *plugin.Host {
	env := cfg.launchEnv()
	var host *plugin.Host
	sockets := &pluginSockets{dir: filepath.Join(cfg.root(), "p"), router: router, log: log, granted: func(id string) []string { return host.Granted(id) }}
	pc := cfg.Plugins
	host = plugin.NewHost(plugin.HostConfig{
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
			out := env.Build(vars)
			// The daemon's variables come last in a launch environment, but
			// a plugin talks to its own socket, not the daemon's.
			if own, ok := vars["HIVE_SOCKET_PATH"]; ok {
				for i, kv := range out {
					if strings.HasPrefix(kv, "HIVE_SOCKET_PATH=") {
						out[i] = "HIVE_SOCKET_PATH=" + own
					}
				}
			}
			return out
		},
		LogDir:        filepath.Join(cfg.root(), "logs", "plugins"),
		Logger:        log,
		Policy:        plugin.Policy{Allow: pc.Allow, Deny: pc.Deny},
		Isolation:     pc.Isolation,
		RestartLimit:  pc.RestartLimit,
		RestartWindow: pc.RestartWindow,
		Hidden:        pc.Hidden,
		Serve:         sockets.serve,
		SocketPath:    cfg.SocketPath,
		Publish:       bus.Publish,
	})
	return host
}

// pluginSockets serves each plugin its own socket. A call there is the
// plugin's: it may call only what its capabilities allow, and the
// plugin.* methods act on it alone.
type pluginSockets struct {
	dir     string
	router  *protocol.Router
	log     *slog.Logger
	granted func(id string) []string
}

func (s *pluginSockets) serve(id string, _ []string) (string, func(), error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256([]byte(id))
	path := filepath.Join(s.dir, hex.EncodeToString(sum[:6])+".sock")
	_ = os.Remove(path) // left by a daemon that crashed
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	if err != nil {
		return "", nil, err
	}
	_ = os.Chmod(path, 0o600)
	ctx, cancel := context.WithCancel(context.Background())
	var (
		mu    sync.Mutex
		conns = map[net.Conn]struct{}{}
		wg    sync.WaitGroup
	)
	h := scopedHandler{id: id, router: s.router, granted: s.granted}
	wg.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			if err := platform.CheckPeer(conn); err != nil {
				s.log.Warn("refused a connection to a plugin socket", "plugin", id, "err", err)
				_ = conn.Close()
				continue
			}
			mu.Lock()
			conns[conn] = struct{}{}
			mu.Unlock()
			wg.Go(func() {
				defer func() {
					mu.Lock()
					delete(conns, conn)
					mu.Unlock()
				}()
				info := protocol.ServerInfo{Version: buildinfo.Get().Version, Capabilities: []string{vt.FrameCapability, vt.FrameLinksCapability, vt.FrameDeflateCapability, vt.FrameClipboardCapability}}
				_ = protocol.ServeConn(ctx, conn, h, info)
			})
		}
	})
	stop := func() {
		cancel()
		_ = ln.Close()
		mu.Lock()
		for c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
		_ = os.Remove(path)
	}
	return path, stop, nil
}

// scopedHandler serves a plugin's socket.
type scopedHandler struct {
	id      string
	router  *protocol.Router
	granted func(id string) []string
}

func (h scopedHandler) Handle(ctx context.Context, req protocol.Request) protocol.Response {
	if !plugin.Allowed(req.Method, h.granted(h.id)) {
		need, ok := plugin.MethodCapability(req.Method)
		msg := fmt.Sprintf("plugin %s may not call %s: no plugin may", h.id, req.Method)
		if ok {
			msg = fmt.Sprintf("plugin %s may not call %s: it needs %s (`hive plugin grant %s %s`)", h.id, req.Method, need, h.id, need)
		}
		return protocol.Fail(req, protocol.NewError(protocol.ErrorCodePermissionDenied, errors.New(msg)))
	}
	return h.router.Handle(plugin.WithCaller(ctx, h.id), req)
}

// Methods lists what the plugin may call, for the welcome.
func (h scopedHandler) Methods() []string {
	granted := h.granted(h.id)
	var out []string
	for _, m := range h.router.Methods() {
		if plugin.Allowed(m, granted) {
			out = append(out, m)
		}
	}
	return out
}

// forwardEvents passes events to plugins with their sequence numbers; a
// loss marker makes every plugin's next event say it missed some.
func forwardEvents(ctx context.Context, bus *event.Bus, host *plugin.Host) {
	sub := bus.Subscribe(event.DefaultBuffer)
	defer sub.Close()
	for {
		ev, err := sub.Next(ctx)
		if err != nil {
			return
		}
		if ev.Type == event.Lost {
			host.Lost()
			continue
		}
		host.EventAt(ev.Type, ev.Seq, ev.Time, ev.Data)
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

// pluginParams name a plugin: required on the daemon's socket, implied
// (and only that plugin) on a plugin's own.
type pluginParams struct {
	Plugin string `json:"plugin,omitempty"`
}

type pluginRunRef struct {
	Plugin string `json:"plugin"`
	Run    string `json:"run"`
}

type pluginRequestParams struct {
	Plugin     string `json:"plugin,omitempty"`
	Capability string `json:"capability"`
	Reason     string `json:"reason,omitempty"`
}

type pluginConfigSet struct {
	Plugin string         `json:"plugin,omitempty"`
	Set    map[string]any `json:"set,omitempty"`
	Unset  []string       `json:"unset,omitempty"`
}

// callerPlugin resolves which plugin a request is about.
func callerPlugin(ctx context.Context, given string) (string, error) {
	caller := plugin.CallerOf(ctx)
	switch {
	case caller != "" && given != "" && given != caller:
		return "", protocol.NewError(protocol.ErrorCodePermissionDenied, fmt.Errorf("plugin %s may only act on itself, not %s", caller, given))
	case caller != "":
		return caller, nil
	case given == "":
		return "", protocol.NewError(protocol.ErrorCodeInvalidParams, errors.New("plugin is required"))
	}
	return given, nil
}

func pluginError(err error) error {
	switch {
	case errors.Is(err, plugin.ErrNotFound), errors.Is(err, plugin.ErrNoAction), errors.Is(err, plugin.ErrNoRun):
		return protocol.NewError(protocol.ErrorCodeNotFound, err)
	case errors.Is(err, plugin.ErrContribution):
		return protocol.NewError(protocol.ErrorCodeInvalidParams, err)
	}
	return err
}

func registerPlugins(r *protocol.Router, host *plugin.Host, store *plugin.Store) {
	r.MustRegister("plugin.list", protocol.Method(func(context.Context, struct{}) ([]plugin.Status, error) {
		return host.Status(), nil
	}))
	r.MustRegister("plugin.reload", protocol.Method(func(_ context.Context, p pluginParams) ([]plugin.Status, error) {
		if p.Plugin == "" {
			return host.Reload(), nil
		}
		st, err := host.ReloadPlugin(p.Plugin)
		if err != nil {
			return nil, pluginError(err)
		}
		return []plugin.Status{st}, nil
	}))
	r.MustRegister("plugin.run", protocol.Method(func(ctx context.Context, p PluginRunRequest) (plugin.Result, error) {
		res, err := host.Action(ctx, p.Plugin, p.Action, plugin.Context{Environment: p.Environment, Tab: p.Tab, Pane: p.Pane})
		return res, pluginError(err)
	}))
	r.MustRegister("plugin.cancel", protocol.Method(func(_ context.Context, p pluginRunRef) (protocol.Empty, error) {
		return protocol.Empty{}, pluginError(host.Cancel(p.Plugin, p.Run))
	}))
	r.MustRegister("plugin.capabilities", protocol.Method(func(ctx context.Context, _ struct{}) (plugin.Discovery, error) {
		return host.Discovery(plugin.CallerOf(ctx)), nil
	}))
	r.MustRegister("plugin.contributions", protocol.Method(func(context.Context, struct{}) (plugin.Contributions, error) {
		return host.Contributions(), nil
	}))
	r.MustRegister("plugin.card", protocol.Method(func(ctx context.Context, u plugin.CardUpdate) (plugin.CardContent, error) {
		id, err := callerPlugin(ctx, u.Plugin)
		if err != nil {
			return plugin.CardContent{}, err
		}
		u.Plugin = id
		c, err := host.SetCard(u)
		return c, pluginError(err)
	}))
	r.MustRegister("plugin.status", protocol.Method(func(ctx context.Context, u plugin.StatusUpdate) (plugin.StatusContent, error) {
		id, err := callerPlugin(ctx, u.Plugin)
		if err != nil {
			return plugin.StatusContent{}, err
		}
		u.Plugin = id
		c, err := host.SetStatus(u)
		return c, pluginError(err)
	}))
	r.MustRegister("plugin.ready", protocol.Method(func(ctx context.Context, p pluginParams) (protocol.Empty, error) {
		id, err := callerPlugin(ctx, p.Plugin)
		if err != nil {
			return protocol.Empty{}, err
		}
		return protocol.Empty{}, pluginError(host.Ready(id))
	}))
	r.MustRegister("plugin.request", protocol.Method(func(ctx context.Context, p pluginRequestParams) (plugin.PermissionRequest, error) {
		id, err := callerPlugin(ctx, p.Plugin)
		if err != nil {
			return plugin.PermissionRequest{}, err
		}
		req, err := host.Request(id, p.Capability, p.Reason)
		if err != nil && !errors.Is(err, plugin.ErrNotFound) {
			err = protocol.NewError(protocol.ErrorCodeInvalidParams, err)
		}
		return req, pluginError(err)
	}))
	r.MustRegister("plugin.config", protocol.Method(func(ctx context.Context, p pluginParams) (plugin.ConfigView, error) {
		id, err := callerPlugin(ctx, p.Plugin)
		if err != nil {
			return plugin.ConfigView{}, err
		}
		v, err := store.Config(ctx, id, buildinfo.Get().Version)
		return v, pluginError(err)
	}))
	r.MustRegister("plugin.config.set", protocol.Method(func(ctx context.Context, p pluginConfigSet) (plugin.ConfigView, error) {
		id, err := callerPlugin(ctx, p.Plugin)
		if err != nil {
			return plugin.ConfigView{}, err
		}
		v, err := store.SetConfig(ctx, id, buildinfo.Get().Version, p.Set, p.Unset)
		if err != nil {
			if !errors.Is(err, plugin.ErrNotFound) {
				err = protocol.NewError(protocol.ErrorCodeInvalidParams, err)
			}
			return v, pluginError(err)
		}
		if plugin.CallerOf(ctx) == "" {
			// The user changed them: restart the plugin on its new settings.
			_, _ = host.ReloadPlugin(id)
		}
		return v, nil
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
