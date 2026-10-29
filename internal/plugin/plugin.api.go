package plugin

import (
	"context"
	"slices"
	"strings"
)

// APIVersion is the newest plugin API this hive implements. SupportedAPIs
// lists every version it runs; a manifest asking for another is refused
// with a clear problem instead of being half-understood.
const APIVersion = 2

// SupportedAPIs are the plugin API versions this hive runs.
var SupportedAPIs = []int{1, 2}

// Capabilities a plugin may hold. hive.* are enforced by the daemon on the
// plugin's own socket. Under plugin API 1, filesystem.write and network are
// enforced only under strict isolation, and process.exec is declared, never
// enforced. Under plugin API 2 a plugin runs sandboxed by default, and the
// sandbox enforces the filesystem, network and program capabilities.
const (
	CapHiveRead        = "hive.read"
	CapHiveWrite       = "hive.write"
	CapHiveInput       = "hive.input"
	CapHiveProcess     = "hive.process"
	CapHiveAdmin       = "hive.admin"
	CapFilesystemWrite = "filesystem.write"
	CapNetwork         = "network"
	CapProcessExec     = "process.exec"

	// Plugin API 2 only.
	CapFilesystemRead           = "filesystem.read"
	CapFilesystemReadWorkspace  = "filesystem.read:workspace"
	CapFilesystemWriteWorkspace = "filesystem.write:workspace"
	// CapUnconfined runs it outside the sandbox: what a manifest's
	// isolation = "none" requires, and what a plugin needs where this
	// system has no sandbox. Approved once per release.
	CapUnconfined = "unconfined"
)

// Enforcement says who keeps a plugin to a capability it lacks.
const (
	EnforcedDaemon   = "daemon"   // every call on the plugin's socket is checked
	EnforcedSandbox  = "sandbox"  // only under strict isolation
	EnforcedDeclared = "declared" // shown to the user; not enforced
)

// CapabilityInfo describes one capability.
type CapabilityInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enforced    string `json:"enforced"`
}

// CapabilityList is every plugin API 1 capability, in the order they are
// shown. It never changes: plugin API 1 and its fixtures depend on it.
var CapabilityList = []CapabilityInfo{
	{CapHiveRead, "read the workspace: environments, tabs, panes, agents, screens, events", EnforcedDaemon},
	{CapHiveWrite, "change the workspace: tabs, panes, layouts, names, metadata, worktrees", EnforcedDaemon},
	{CapHiveInput, "type into terminals and prompt agents", EnforcedDaemon},
	{CapHiveProcess, "start and stop processes and agents through Hive", EnforcedDaemon},
	{CapHiveAdmin, "run other plugins, restore sessions, debug the daemon", EnforcedDaemon},
	{CapFilesystemWrite, "write files outside the plugin's own directories", EnforcedSandbox},
	{CapNetwork, "use the network", EnforcedSandbox},
	{CapProcessExec, "run other programs", EnforcedDeclared},
}

// CapabilityListV2 is every plugin API 2 capability: the sandbox enforces
// files, network and programs.
var CapabilityListV2 = []CapabilityInfo{
	{CapHiveRead, "read the workspace: environments, tabs, panes, agents, screens, events", EnforcedDaemon},
	{CapHiveWrite, "change the workspace: tabs, panes, layouts, names, metadata, worktrees", EnforcedDaemon},
	{CapHiveInput, "type into terminals and prompt agents", EnforcedDaemon},
	{CapHiveProcess, "start and stop processes and agents through Hive", EnforcedDaemon},
	{CapHiveAdmin, "run other plugins, restore sessions, debug the daemon", EnforcedDaemon},
	{CapFilesystemRead, "read files outside the plugin's own directories and the system's", EnforcedSandbox},
	{CapFilesystemReadWorkspace, "read the directories of your environments", EnforcedSandbox},
	{CapFilesystemWrite, "write files outside the plugin's own directories", EnforcedSandbox},
	{CapFilesystemWriteWorkspace, "write in the directories of your environments", EnforcedSandbox},
	{CapNetwork, "use the network (only the hosts it lists, when it lists any)", EnforcedSandbox},
	{CapProcessExec, "run other programs (only those it lists, when it lists any)", EnforcedSandbox},
	{CapUnconfined, "run outside the sandbox, with everything you can reach", EnforcedSandbox},
}

// CapabilitiesFor is the capability list of a plugin API version.
func CapabilitiesFor(api int) []CapabilityInfo {
	if api >= 2 {
		return CapabilityListV2
	}
	return CapabilityList
}

// KnownCapability reports whether name is a capability of any plugin API.
func KnownCapability(name string) bool {
	return knownIn(CapabilityList, name) || knownIn(CapabilityListV2, name)
}

// KnownCapabilityFor reports whether name is a capability of plugin API
// api.
func KnownCapabilityFor(api int, name string) bool { return knownIn(CapabilitiesFor(api), name) }

func knownIn(list []CapabilityInfo, name string) bool {
	return slices.ContainsFunc(list, func(c CapabilityInfo) bool { return c.Name == name })
}

// AllCapabilities lists every capability's name, of every plugin API.
func AllCapabilities() []string {
	out := capabilityNames(CapabilityList)
	for _, c := range CapabilityListV2 {
		if !slices.Contains(out, c.Name) {
			out = append(out, c.Name)
		}
	}
	return out
}

func capabilityNames(list []CapabilityInfo) []string {
	out := make([]string, len(list))
	for i, c := range list {
		out[i] = c.Name
	}
	return out
}

// capSelf marks methods every plugin may call on its own socket: its own
// contributions, readiness, permission requests and settings, and
// discovery. They act on the calling plugin only.
const capSelf = "self"

// methodCapabilities maps every daemon method to the capability a plugin
// needs to call it. A method missing here is refused on plugin sockets, so
// new methods are closed until classified (a test lists them).
var methodCapabilities = map[string]string{
	// Discovery and the plugin's own surface.
	"api.schema": capSelf, "runtime.ping": capSelf, "client.report": capSelf,
	"plugin.capabilities": capSelf, "plugin.card": capSelf, "plugin.status": capSelf,
	"plugin.ready": capSelf, "plugin.request": capSelf, "plugin.config": capSelf, "plugin.config.set": capSelf,

	// Reading.
	"agent.explain": CapHiveRead, "agent.get": CapHiveRead, "agent.list": CapHiveRead, "agent.manifests": CapHiveRead,
	"agent.read": CapHiveRead, "agent.wait": CapHiveRead, "api.snapshot": CapHiveRead,
	"environment.get": CapHiveRead, "environment.list": CapHiveRead, "events.subscribe": CapHiveRead,
	"layout.export": CapHiveRead, "metadata.list": CapHiveRead, "pane.get": CapHiveRead, "pane.list": CapHiveRead,
	"pane.read": CapHiveRead, "pane.wait_output": CapHiveRead, "process.get": CapHiveRead, "process.list": CapHiveRead,
	"process.logs": CapHiveRead, "runtime.status": CapHiveRead, "tab.get": CapHiveRead, "tab.list": CapHiveRead,
	"terminal.snapshot": CapHiveRead, "terminal.frames": CapHiveRead, "terminal.graphics": CapHiveRead,
	"worktree.list": CapHiveRead, "plugin.list": CapHiveRead, "plugin.contributions": CapHiveRead,
	"issue.get": CapHiveRead, "issue.search": CapHiveRead, "issue.comment": CapHiveWrite,
	"plugin.views": CapHiveRead, "plugin.view.set": CapHiveWrite, "plugin.view.clear": CapHiveWrite,
	"observer.activity": CapHiveRead, "task.list": CapHiveRead, "task.get": CapHiveRead, "task.wait": CapHiveRead,
	"branch.list": CapHiveRead, "branch.get": CapHiveRead, "branch.check": CapHiveRead, "branch.diff": CapHiveRead,
	"branch.conflicts": CapHiveRead, "workflow.list": CapHiveRead, "workflow.get": CapHiveRead,
	"workflow.validate": CapHiveRead, "workflow.runs": CapHiveRead, "workflow.status": CapHiveRead,
	"workflow.wait": CapHiveRead, "insights.summary": CapHiveRead, "insights.asks": CapHiveRead,
	"insights.budgets": CapHiveRead, "insights.export": CapHiveRead,

	// Changing the workspace.
	"agent.focus": CapHiveWrite, "agent.rename": CapHiveWrite, "agent.seen": CapHiveWrite, "agent.report": CapHiveWrite,
	"environment.create": CapHiveWrite, "environment.update": CapHiveWrite, "layout.apply": CapHiveWrite,
	"metadata.report": CapHiveWrite, "pane.close": CapHiveWrite, "pane.focus": CapHiveWrite, "pane.move": CapHiveWrite,
	"pane.popup": CapHiveWrite, "pane.rename": CapHiveWrite, "pane.resize": CapHiveWrite, "pane.split": CapHiveWrite,
	"pane.swap": CapHiveWrite, "pane.zoom": CapHiveWrite, "tab.close": CapHiveWrite, "tab.create": CapHiveWrite,
	"tab.focus": CapHiveWrite, "tab.rename": CapHiveWrite, "tab.resize": CapHiveWrite, "terminal.resize": CapHiveWrite,
	"session.save": CapHiveWrite, "worktree.create": CapHiveWrite, "worktree.open": CapHiveWrite,
	"insights.usage": CapHiveWrite,

	// Typing into terminals.
	"agent.prompt": CapHiveInput, "agent.send_keys": CapHiveInput, "pane.input": CapHiveInput,
	"pane.send_keys": CapHiveInput, "pane.send_text": CapHiveInput, "terminal.input": CapHiveInput,
	"terminal.attach": CapHiveInput, "terminal.image": CapHiveInput, "branch.comment": CapHiveInput,
	"branch.send": CapHiveInput,

	// Starting and stopping programs, and removing what holds work.
	"agent.start": CapHiveProcess, "agent.stop": CapHiveProcess, "process.start": CapHiveProcess,
	"process.stop": CapHiveProcess, "pane.run": CapHiveProcess, "environment.remove": CapHiveProcess,
	"worktree.remove": CapHiveProcess, "task.run": CapHiveProcess, "task.cancel": CapHiveProcess,
	"task.retry": CapHiveProcess, "task.approve": CapHiveProcess, "branch.ready": CapHiveProcess,
	"branch.update": CapHiveProcess, "branch.clean": CapHiveProcess, "workflow.run": CapHiveProcess,
	"workflow.rerun": CapHiveProcess, "workflow.cancel": CapHiveProcess, "workflow.fire": CapHiveProcess,
	"workflow.trigger": CapHiveWrite,

	// Administration.
	"debug.pprof": CapHiveAdmin, "debug.stats": CapHiveAdmin, "runtime.gc": CapHiveAdmin,
	"session.restore": CapHiveAdmin, "plugin.reload": CapHiveAdmin, "plugin.run": CapHiveAdmin,
	"plugin.cancel": CapHiveAdmin, "agent.reload": CapHiveAdmin, "workflow.reload": CapHiveAdmin,
}

// closedMethods are daemon methods no plugin may call, whatever it holds:
// an observer credential a plugin made would outlive the plugin and its
// grants, a push or a pull request is published only on a person's own
// command, only a person decides which shared workflows run by
// themselves, and only a person lifts a budget's hold on spending.
var closedMethods = []string{
	"observer.audit", "observer.create", "observer.get", "observer.list", "observer.revoke", "observer.validate",
	"branch.push", "branch.pr", "workflow.trust", "insights.resume",
	// A click is a person's: a plugin runs its own actions directly.
	"plugin.view.click",
	// A plugin closes no tracker's work: issues close when a task merges,
	// or by a person.
	"issue.close",
}

// Closed reports whether method is classified as callable by no plugin.
func Closed(method string) bool { return slices.Contains(closedMethods, method) }

// MethodCapability returns the capability a plugin needs for method; ok
// is false for a method no plugin may call.
func MethodCapability(method string) (capability string, ok bool) {
	c, ok := methodCapabilities[method]
	return c, ok
}

// Allowed reports whether a plugin holding granted may call method.
func Allowed(method string, granted []string) bool {
	c, ok := methodCapabilities[method]
	return ok && (c == capSelf || slices.Contains(granted, c))
}

// ClassifiedMethods lists the methods with a capability, and the closed
// ones, sorted.
func ClassifiedMethods() []string {
	out := make([]string, 0, len(methodCapabilities)+len(closedMethods))
	for m := range methodCapabilities {
		out = append(out, m)
	}
	out = append(out, closedMethods...)
	slices.Sort(out)
	return out
}

// Events are the stable event types of plugin API 1: their names and data
// shapes do not change within the version (docs/plugins/api.md). Handlers
// may name others (or prefixes); events Hive does not publish never fire.
var Events = []string{
	"agent.state",
	"environment.created", "environment.git", "environment.removed", "environment.updated",
	"layout.updated",
	"metadata.changed",
	"pane.closed", "pane.created", "pane.focused", "pane.moved", "pane.updated",
	"plugin.changed", "plugin.contributions", "plugin.permission",
	"process.exited", "process.output", "process.recovered", "process.started",
	"session.restored",
	"tab.closed", "tab.created", "tab.updated",
	"worktree.created", "worktree.removed",
}

// Plugin events, published on the daemon's bus.
const (
	// EventChanged: a plugin's health changed (data: Status).
	EventChanged = "plugin.changed"
	// EventContributions: cards or status indicators changed (data:
	// {"plugin": id}).
	EventContributions = "plugin.contributions"
	// EventPermission: a plugin asked for a capability (data:
	// PermissionRequest).
	EventPermission = "plugin.permission"
)

// KnownEvent reports whether a handler's on value can ever match: a
// catalogued type, or a prefix of one.
func KnownEvent(on string) bool {
	if prefix, ok := strings.CutSuffix(on, "."); ok {
		return slices.ContainsFunc(Events, func(e string) bool { return strings.HasPrefix(e, prefix+".") })
	}
	return slices.Contains(Events, on)
}

// Contribution kinds a plugin may declare under [contributes].
const (
	ContribCard    = "card"
	ContribStatus  = "status"
	ContribAction  = "action"
	ContribPalette = "palette"
	ContribLink    = "link"
	ContribConfig  = "config" // its settings form, from [configuration]
)

// ContributionKinds lists the kinds.
var ContributionKinds = []string{ContribCard, ContribStatus, ContribAction, ContribPalette, ContribLink, ContribConfig}

// Discovery is what a plugin can learn about the hive it runs in
// (plugin.capabilities, and HIVE_PLUGIN_API).
type Discovery struct {
	API           int              `json:"api"`
	SupportedAPIs []int            `json:"supported_apis"`
	HiveVersion   string           `json:"hive_version"`
	Capabilities  []CapabilityInfo `json:"capabilities"`
	Events        []string         `json:"events"`
	Contributions []string         `json:"contributions"`
	// Isolation lists the execution modes available here.
	Isolation []string `json:"isolation"`
	// Plugin, Granted and Methods describe the calling plugin, on its own
	// socket: what it holds and every method it may call.
	Plugin  string   `json:"plugin,omitempty"`
	Granted []string `json:"granted,omitempty"`
	Methods []string `json:"methods,omitempty"`
}

type callerKey struct{}

// WithCaller marks ctx as a request from plugin id on its own socket.
func WithCaller(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, callerKey{}, id)
}

// CallerOf returns the plugin whose socket a request came on ("" for the
// daemon's own socket: the user, the CLI, the UI).
func CallerOf(ctx context.Context) string {
	id, _ := ctx.Value(callerKey{}).(string)
	return id
}
