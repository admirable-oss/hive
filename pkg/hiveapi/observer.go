package hiveapi

import (
	"context"
	"errors"
	"net"
	"os"

	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/remote"
)

// Observers: read-only access for dashboards and notifiers.
//
//	o, err := hiveapi.Observe(ctx, hiveapi.ObserverOptions{}) // token from $HIVE_OBSERVER_TOKEN
//	snap, err := o.Snapshot(ctx)
//	for _, a := range snap.Agents { fmt.Println(a.Name, a.State) }
//
// An observer connects to the daemon's observer socket with a token from
// `hive observer create`, and sees only what the token's scope allows. It
// cannot type into terminals, change layouts or start anything: the
// observer socket has no such methods. Follow keeps a view current across
// reconnects. docs/observers.md describes the API and its guarantees.

// Observer types.
type (
	Observer            = observer.Client
	ObserverScope       = observer.Scope
	ObserverCredential  = observer.Credential
	ObserverStatus      = observer.Status
	ObserverSnapshot    = observer.Snapshot
	ObserverEvent       = observer.Event
	ObserverEventStream = observer.EventStream
	ObserverPage        = observer.Page
	ObserverUpdate      = observer.Update
	ObservedEnvironment = observer.Environment
	ObservedTab         = observer.Tab
	ObservedPane        = observer.Pane
	ObservedProcess     = observer.Process
	ObservedAgent       = observer.Agent
	ObservedMetadata    = observer.Metadata
	ObservedPaneDetail  = observer.PaneResult
	Screen              = observer.Screen
	ScreenUpdate        = observer.ScreenUpdate
	ScreenStream        = observer.ScreenStream
	ScreenRequest       = observer.ScreenRequest
	OutputRequest       = observer.OutputRequest
	OutputResult        = observer.OutputResult
	HistoryRequest      = observer.HistoryRequest
	EventsRequest       = observer.EventsRequest
	WatchRequest        = observer.WatchRequest
)

// ObserverResync is the event type that says events were missed: take a
// new snapshot. Follow handles it.
const ObserverResync = observer.Resync

// ObserverTokenEnv is where Observe finds a token when none is given.
const ObserverTokenEnv = "HIVE_OBSERVER_TOKEN"

// ObserverOptions say which observer socket to reach, and with what token.
// The zero value uses $HIVE_OBSERVER_TOKEN on this machine's session, found
// as Options does.
type ObserverOptions struct {
	// Token is the observer credential's token (default
	// $HIVE_OBSERVER_TOKEN).
	Token string
	// Socket is the observer socket; it overrides Session and Home.
	Socket  string
	Session string
	Home    string
	// Host reaches the observer socket of Hive on another machine
	// through ssh (`ssh <Host> hive proxy --observer`); RemoteSession
	// names its session.
	Host          string
	RemoteSession string
	// Dial connects some other way; it overrides the rest.
	Dial func(ctx context.Context) (net.Conn, error)
	// Name identifies this program in the daemon's audit log.
	Name string
}

// SocketPath resolves where this machine's observer socket is.
func (o ObserverOptions) SocketPath() (string, error) {
	if o.Socket != "" {
		return o.Socket, nil
	}
	root, err := sessionRoot(o.Home, o.Session)
	if err != nil {
		return "", err
	}
	return platform.SocketPath(root, "observer.sock"), nil
}

func (o ObserverOptions) config() (observer.ClientConfig, error) {
	cfg := observer.ClientConfig{Token: o.Token, Name: o.Name, Dial: o.Dial}
	if cfg.Token == "" {
		cfg.Token = os.Getenv(ObserverTokenEnv)
	}
	if cfg.Token == "" {
		return cfg, errors.New("hiveapi: no observer token (ObserverOptions.Token or $" + ObserverTokenEnv + ")")
	}
	if cfg.Name == "" {
		cfg.Name = "hiveapi-observer"
	}
	switch {
	case cfg.Dial != nil:
	case o.Host != "":
		cfg.Dial = remote.Target{Host: o.Host, Session: o.RemoteSession}.DialObserver
	default:
		sock, err := o.SocketPath()
		if err != nil {
			return cfg, err
		}
		cfg.Socket = sock
	}
	return cfg, nil
}

// Observe connects to the observer socket and authenticates.
func Observe(ctx context.Context, opts ObserverOptions) (*Observer, error) {
	cfg, err := opts.config()
	if err != nil {
		return nil, err
	}
	return observer.Connect(ctx, cfg)
}

// Follow keeps an observer's view current until ctx ends or the token stops
// working: fn gets a snapshot first, then each event after it, and a new
// snapshot whenever events were missed (the observer fell behind, the
// connection dropped for longer than the daemon's history, the daemon
// restarted). It reconnects by itself.
func Follow(ctx context.Context, opts ObserverOptions, fn func(ObserverUpdate) error) error {
	cfg, err := opts.config()
	if err != nil {
		return err
	}
	return observer.Follow(ctx, cfg, fn)
}
