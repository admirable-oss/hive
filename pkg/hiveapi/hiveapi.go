// Package hiveapi is the Go SDK for Hive: it drives a running Hive daemon
// (the one `hive` talks to) over its socket, so Go programs can start coding
// agents, prompt them, wait for them and read what they did.
//
//	c, err := hiveapi.Connect(hiveapi.Options{})
//	if err != nil { … }
//	defer c.Close()
//	started, err := c.StartAgent(ctx, hiveapi.StartAgent{Kind: "codex", EnvironmentID: "api", Worktree: "fix/login"})
//	res, err := c.Prompt(ctx, started.Agent.ID, "Fix the login redirect bug", hiveapi.PromptOptions{Wait: true, Read: 40})
//	fmt.Println(res.Outcome, strings.Join(res.Output, "\n"))
//
// Connect finds the daemon as the hive command does: $HIVE_SOCKET_PATH
// inside a Hive pane, else the session ($HIVE_SESSION, default "default")
// under $HIVE_HOME (default ~/.hive). It does not start a daemon; run any
// hive command (or `hive daemon start`) first.
//
// IDs: an agent is named by its process ID or its pane's ID; both work
// wherever an agent ID is asked for. Methods mirror the daemon's API, which
// `hive api schema` describes; Call reaches any method directly.
package hiveapi

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/session"
)

// The API's types.
type (
	Agent        = agent.Agent
	AgentStatus  = agent.Status
	State        = agent.State
	Until        = agent.Until
	Outcome      = agent.Outcome
	Rollups      = agent.Rollups
	Explanation  = agent.Explanation
	Manifest     = agent.Manifest
	PromptResult = agent.PromptResult
	WaitResult   = agent.WaitResult
	Environment  = environment.Environment
	Tab          = pane.Tab
	Pane         = pane.Pane
	Spec         = pane.Spec
	Process      = process.Process
	Event        = event.Event
	EventStream  = client.EventStream
	Snapshot     = client.APISnapshot
	Schema       = protocol.Schema
	// StartAgent starts an agent by its manifest; see Client.StartAgent.
	StartAgent   = client.AgentStartRequest
	AgentStarted = client.AgentStarted
	// PromptOptions and WaitOptions tune Prompt and Wait.
	PromptOptions = client.PromptOptions
	WaitOptions   = client.WaitOptions
	ReadOptions   = client.ReadRequest
	// Error is an error the daemon answered with; Code is one of the Code*
	// constants.
	Error = protocol.Error
)

// Agent states.
const (
	StateUnknown = agent.StateUnknown
	StateWorking = agent.StateWorking
	StateBlocked = agent.StateBlocked
	StateDone    = agent.StateDone
	StateIdle    = agent.StateIdle
	StateExited  = agent.StateExited
)

// What Wait (and Prompt with Wait) waits for.
const (
	UntilIdle    = agent.UntilIdle
	UntilDone    = agent.UntilDone
	UntilWorking = agent.UntilWorking
	UntilBlocked = agent.UntilBlocked
	UntilExited  = agent.UntilExited
	UntilChange  = agent.UntilChange
)

// How a prompt or wait ended.
const (
	OutcomeReached = agent.OutcomeReached
	OutcomeStarted = agent.OutcomeStarted
	OutcomeBlocked = agent.OutcomeBlocked
	OutcomeExited  = agent.OutcomeExited
	OutcomeTimeout = agent.OutcomeTimeout
	OutcomeStalled = agent.OutcomeStalled
)

// Error codes.
const (
	CodeNotFound      = protocol.ErrorCodeNotFound
	CodeInvalidParams = protocol.ErrorCodeInvalidParams
	CodeConflict      = protocol.ErrorCodeConflict // e.g. prompting a blocked agent
	CodeUnavailable   = protocol.ErrorCodeUnavailable
	CodeTimeout       = protocol.ErrorCodeTimeout
)

// ErrUnavailable means no daemon answers on the socket.
var ErrUnavailable = client.ErrUnavailable

// Options say which daemon to connect to. The zero value finds it as the
// hive command does.
type Options struct {
	// Socket is the daemon's socket; it overrides everything else.
	Socket string
	// Session names the session (default $HIVE_SESSION, else "default").
	Session string
	// Home is where sessions live (default $HIVE_HOME, else ~/.hive).
	Home string
	// Name identifies this client in the daemon's logs.
	Name string
}

// SocketPath resolves where the daemon listens.
func (o Options) SocketPath() (string, error) {
	if o.Socket != "" {
		return o.Socket, nil
	}
	if o.Session == "" && o.Home == "" {
		if p := os.Getenv("HIVE_SOCKET_PATH"); p != "" {
			return p, nil
		}
	}
	home := o.Home
	if home == "" {
		home = os.Getenv("HIVE_HOME")
	}
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(h, ".hive")
	}
	name := o.Session
	if name == "" {
		name = os.Getenv(session.EnvVar)
	}
	root, err := session.Root(home, name)
	if err != nil {
		return "", err
	}
	return platform.SocketPath(root, "hive.sock"), nil
}

// Client is a connection to a Hive daemon. It is safe for concurrent use.
type Client struct {
	c      client.Client
	agents client.Agents
	ws     client.Workspace
}

// Connect connects to the daemon and checks that it answers.
func Connect(opts Options) (*Client, error) {
	return ConnectContext(context.Background(), opts)
}

// ConnectContext is Connect bounded by ctx.
func ConnectContext(ctx context.Context, opts Options) (*Client, error) {
	sock, err := opts.SocketPath()
	if err != nil {
		return nil, err
	}
	name := opts.Name
	if name == "" {
		name = "hiveapi"
	}
	c := client.NewService(client.Config{SocketPath: sock, Name: name})
	if err := c.Ping(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return New(c), nil
}

// New wraps an existing connection (the hive command's own).
func New(c client.Client) *Client {
	return &Client{c: c, agents: client.NewAgents(c), ws: client.NewWorkspace(c)}
}

// Close closes the connection; agents keep running.
func (c *Client) Close() error { return c.c.Close() }

// Call performs any daemon method (see `hive api schema`), decoding its
// result into result (which may be nil).
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	return c.c.Call(ctx, method, params, result)
}

// IsCode reports whether err is a daemon error with that code.
func IsCode(err error, code string) bool {
	var e *protocol.Error
	return errors.As(err, &e) && e.Code == code
}

// --- agents ---

// Agents lists the agents: terminal processes a manifest recognises (all
// terminals with all).
func (c *Client) Agents(ctx context.Context, all bool) ([]Agent, error) {
	res, err := c.agents.List(ctx, all)
	return res.Agents, err
}

// Agent returns one agent, by process or pane ID.
func (c *Client) Agent(ctx context.Context, id string) (Agent, error) { return c.agents.Get(ctx, id) }

// StartAgent starts an agent by its manifest in a new tab; with Worktree,
// on that branch in a new git worktree with its own environment.
func (c *Client) StartAgent(ctx context.Context, req StartAgent) (AgentStarted, error) {
	return c.agents.Start(ctx, req)
}

// Prompt pastes a prompt into an agent and sends it. It returns once the
// agent reacts, or with opts.Wait once it finishes; the outcome says how it
// ended. A blocked agent is refused with CodeConflict.
func (c *Client) Prompt(ctx context.Context, id, text string, opts PromptOptions) (PromptResult, error) {
	return c.agents.Prompt(ctx, id, text, opts)
}

// Wait waits for an agent's state; it also ends when the agent blocks or
// exits.
func (c *Client) Wait(ctx context.Context, id string, opts WaitOptions) (WaitResult, error) {
	return c.agents.Wait(ctx, id, opts)
}

// Read returns an agent's text: the screen by default; see ReadOptions.
func (c *Client) Read(ctx context.Context, id string, opts ReadOptions) ([]string, error) {
	return c.agents.Read(ctx, id, opts)
}

// SendKeys presses named keys in an agent (Enter, Escape, Up, C-c, y, …).
func (c *Client) SendKeys(ctx context.Context, id string, keys ...string) error {
	return c.agents.SendKeys(ctx, id, keys)
}

// Explain shows why an agent is in its state.
func (c *Client) Explain(ctx context.Context, id string) (Explanation, error) {
	return c.agents.Explain(ctx, id)
}

// RenameAgent renames an agent's pane.
func (c *Client) RenameAgent(ctx context.Context, id, name string) (Agent, error) {
	return c.agents.Rename(ctx, id, name)
}

// FocusAgent shows an agent's pane in every client.
func (c *Client) FocusAgent(ctx context.Context, id string) (Agent, error) {
	return c.agents.Focus(ctx, id)
}

// StopAgent stops an agent's process; its pane stays with its last screen.
func (c *Client) StopAgent(ctx context.Context, id string) (Agent, error) {
	return c.agents.Stop(ctx, id)
}

// Manifests lists the agents Hive can recognise and start.
func (c *Client) Manifests(ctx context.Context) ([]Manifest, error) { return c.agents.Manifests(ctx) }

// --- workspace ---

// Snapshot reads every environment, tab, pane, process and agent at once.
func (c *Client) Snapshot(ctx context.Context) (Snapshot, error) { return c.agents.Snapshot(ctx) }

// Schema describes the daemon's API.
func (c *Client) Schema(ctx context.Context) (Schema, error) { return c.agents.Schema(ctx) }

// Environments lists the environments.
func (c *Client) Environments(ctx context.Context) ([]Environment, error) {
	return c.c.EnvironmentList(ctx)
}

// CreateEnvironment creates an environment rooted at dir (an existing
// directory; empty for a managed one under Hive's home).
func (c *Client) CreateEnvironment(ctx context.Context, id, dir string, vars map[string]string) (Environment, error) {
	return c.c.EnvironmentCreate(ctx, environment.CreateRequest{ID: id, Root: dir, Env: vars})
}

// Panes lists panes, optionally of one environment or tab.
func (c *Client) Panes(ctx context.Context, envID, tabID string) ([]Pane, error) {
	return c.ws.PaneList(ctx, envID, tabID)
}

// ReadPane returns a pane's text.
func (c *Client) ReadPane(ctx context.Context, id string, opts ReadOptions) ([]string, error) {
	return c.ws.PaneRead(ctx, id, opts)
}

// RunInPane types a command line into a pane and presses Enter.
func (c *Client) RunInPane(ctx context.Context, id, command string) error {
	return c.ws.PaneRun(ctx, id, command)
}

// Events subscribes to daemon events whose type starts with one of types
// (all when none): agent.state, process.exited, pane.created, …
func (c *Client) Events(ctx context.Context, types ...string) (*EventStream, error) {
	return c.c.Events(ctx, types...)
}
