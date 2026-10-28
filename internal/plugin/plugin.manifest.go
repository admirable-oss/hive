// Package plugin extends Hive with programs it runs for the user: a plugin
// is a directory with a hive-plugin.toml manifest. The daemon runs its
// [[startup]] commands while it runs, its [[events]] commands when a
// matching event happens, and its [[actions]] when asked (from the CLI, a
// key binding or the UI). Plugins talk back to Hive through the hive CLI
// ($HIVE_BIN), like any agent in a pane: report-metadata for sidebar
// tokens, agent start for new agents, and so on.
//
// Every command gets HIVE_PLUGIN_ID, HIVE_PLUGIN_DIR, HIVE_PLUGIN_DATA (a
// persistent directory it may write), HIVE_PLUGIN_CONFIG (a versioned
// settings file), HIVE_PLUGIN_CONFIG_DIR, and HIVE_PLUGIN_VERSION, plus a context JSON
// (what triggered it: the event, the action, the focused pane) on stdin
// and in the file $HIVE_PLUGIN_CONTEXT.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// ManifestFile is the manifest's name in a plugin's directory.
const ManifestFile = "hive-plugin.toml"

// Manifest describes a plugin.
type Manifest struct {
	// API is the plugin API version the plugin is written for (see
	// APIVersion). Zero is a plugin from before versioned APIs: API 1,
	// holding every capability, with no contributions.
	API         int    `toml:"api" json:"api,omitempty"`
	ID          string `toml:"id" json:"id"`
	Name        string `toml:"name" json:"name,omitempty"`
	Version     string `toml:"version" json:"version"`
	Description string `toml:"description" json:"description,omitempty"`
	// MinHiveVersion is the oldest hive the plugin works with.
	MinHiveVersion string `toml:"min_hive_version" json:"min_hive_version,omitempty"`
	// MaxHiveVersion is the newest hive version the plugin supports.
	MaxHiveVersion string `toml:"max_hive_version" json:"max_hive_version,omitempty"`
	// DataDirectory selects a relative subdirectory under the persistent
	// per-plugin data root. Empty and "." preserve the historical root path.
	DataDirectory string `toml:"data_directory" json:"data_directory,omitempty"`
	// Configuration describes settings stored separately from plugin data.
	Configuration Configuration `toml:"configuration" json:"configuration,omitempty"`
	// Migrations upgrade settings when Configuration.Version advances.
	Migrations []Migration `toml:"migrations" json:"migrations,omitempty"`
	// Platforms lists the systems it runs on (darwin, linux, windows);
	// empty: all. Plugins whose commands are shell scripts should leave
	// windows out.
	Platforms []string `toml:"platforms" json:"platforms,omitempty"`
	// Runtimes names executables needed to run the published plugin artifact.
	// Plugin releases should include built output so installation never has to
	// download packages or invoke a compiler.
	Runtimes []Runtime `toml:"runtime" json:"runtime,omitempty"`
	// Test is how `hive plugin test` and `publish` check the plugin (Hive
	// 1.7 and later; earlier releases reject the key, so a manifest with it
	// needs min_hive_version 1.7.0).
	Test *TestSpec `toml:"test" json:"test,omitempty"`

	// Permissions are the capabilities it asks for (API 1).
	Permissions Permissions `toml:"permissions" json:"permissions,omitzero"`
	// Contributes is what it adds to the UI (API 1).
	Contributes Contributes `toml:"contributes" json:"contributes,omitzero"`
	// Isolation "strict" says it can run sandboxed: no network or writes
	// outside its own directories unless granted, and only its own socket.
	// Plugin API 2 runs sandboxed by default; "none" asks to run outside
	// the sandbox, with IsolationReason shown to whoever installs it.
	Isolation       string `toml:"isolation" json:"isolation,omitempty"`
	IsolationReason string `toml:"isolation_reason" json:"isolation_reason,omitempty"`
	// EventConcurrency bounds its event commands running at once (default
	// 4); 1 runs them strictly one after another, in event order.
	EventConcurrency int `toml:"event_concurrency" json:"event_concurrency,omitempty"`

	Startup []Startup `toml:"startup" json:"startup,omitempty"`
	Actions []Action  `toml:"actions" json:"actions,omitempty"`
	Events  []Event   `toml:"events" json:"events,omitempty"`
}

// Permissions are the capabilities a plugin asks for. Required ones are
// granted when the user installs it; optional ones only when the user
// grants them (or allows them in [plugins] allow).
type Permissions struct {
	Required []string `toml:"required" json:"required,omitempty"`
	Optional []string `toml:"optional" json:"optional,omitempty"`
	// Reasons say why, per capability; shown when installing.
	Reasons map[string]string `toml:"reasons" json:"reasons,omitempty"`
	// Network and Process narrow network and process.exec (plugin API 2):
	// the hosts it may reach, the programs it may run.
	Network NetworkScope `toml:"network" json:"network,omitzero"`
	Process ProcessScope `toml:"process" json:"process,omitzero"`
}

// NetworkScope lists the hosts a plugin API 2 plugin may reach, as
// host:port ("api.linear.app:443", "*.example.org:443"). None listed, a
// plugin granted network reaches any host.
type NetworkScope struct {
	Hosts []string `toml:"hosts" json:"hosts,omitempty"`
}

// ProcessScope lists the programs a plugin API 2 plugin may run: names
// found on PATH when it starts, or absolute paths. None listed, a plugin
// granted process.exec runs any program.
type ProcessScope struct {
	Exec []string `toml:"exec" json:"exec,omitempty"`
}

// Contributes is a plugin's restricted UI contribution: declared here, so
// the UI draws it from data and never runs plugin code to draw.
type Contributes struct {
	// Cards are sidebar cards whose rows the plugin sets at run time.
	Cards []Card `toml:"cards" json:"cards,omitempty"`
	// Status are tab-bar indicators whose text the plugin sets.
	Status []StatusIndicator `toml:"status" json:"status,omitempty"`
	// Actions offer its actions where the user is: on a pane, an agent, an
	// environment or the session.
	Actions []ContextAction `toml:"actions" json:"actions,omitempty"`
	// Palette puts its actions in the command palette.
	Palette []PaletteEntry `toml:"palette" json:"palette,omitempty"`
	// Links are fixed URLs offered in the palette.
	Links []Link `toml:"links" json:"links,omitempty"`
}

// Card is a sidebar card, about the focused environment or agent.
type Card struct {
	ID    string `toml:"id" json:"id"`
	Title string `toml:"title" json:"title"`
	// Scope is environment (default) or agent.
	Scope string `toml:"scope" json:"scope,omitempty"`
}

// StatusIndicator is a short text on the tab bar.
type StatusIndicator struct {
	ID    string `toml:"id" json:"id"`
	Title string `toml:"title" json:"title"`
}

// ContextAction offers an action in the contexts it names.
type ContextAction struct {
	Action string `toml:"action" json:"action"`
	Title  string `toml:"title" json:"title,omitempty"`
	// Contexts are pane, agent, environment and session (default all).
	Contexts []string `toml:"contexts" json:"contexts,omitempty"`
}

// PaletteEntry puts an action in the command palette.
type PaletteEntry struct {
	Action string `toml:"action" json:"action"`
	Title  string `toml:"title" json:"title,omitempty"`
}

// Link is a fixed http(s) URL.
type Link struct {
	ID    string `toml:"id" json:"id,omitempty"`
	Title string `toml:"title" json:"title"`
	URL   string `toml:"url" json:"url"`
}

// Contribution limits keep the UI readable.
const (
	MaxCards          = 4
	MaxStatus         = 4
	MaxContextActions = 16
	MaxPalette        = 16
	MaxLinks          = 8
	MaxTitle          = 48
)

// Action contexts.
var ActionContexts = []string{"pane", "agent", "environment", "session"}

// Runtime is an executable requirement checked before installation and when
// loading an installed plugin.
type Runtime struct {
	// Kind "wasm" (plugin API 2) says its commands are WebAssembly modules
	// in its package, run by Hive itself; Command is then empty.
	Kind       string `toml:"kind" json:"kind,omitempty"`
	Command    string `toml:"command" json:"command"`
	MinVersion string `toml:"min_version" json:"min_version,omitempty"`
}

// TestSpec is a plugin's own tests: a command run in its directory, with
// HIVE_BIN and HIVE_HOME pointing at the Hive under test.
type TestSpec struct {
	Command []string `toml:"command" json:"command"`
	// Timeout bounds it (default 10m).
	Timeout string `toml:"timeout" json:"timeout,omitempty"`
}

// TestTimeout is the test's timeout.
func (t *TestSpec) TestTimeout() time.Duration {
	if t == nil || t.Timeout == "" {
		return 10 * time.Minute
	}
	d, _ := time.ParseDuration(t.Timeout)
	return d
}

// Configuration defines a versioned settings file stored in Hive's
// per-plugin configuration directory.
type Configuration struct {
	Version   int       `toml:"version" json:"version,omitempty"`
	Directory string    `toml:"directory" json:"directory,omitempty"`
	Settings  []Setting `toml:"settings" json:"settings,omitempty"`
}

// Setting declares one user-editable setting. Secret types are intentionally
// not supported: this file is plaintext and must not be used for credentials.
type Setting struct {
	Name        string `toml:"name" json:"name"`
	Type        string `toml:"type" json:"type"`
	Description string `toml:"description" json:"description,omitempty"`
	Required    bool   `toml:"required" json:"required,omitempty"`
	Default     any    `toml:"default,omitempty" json:"default,omitempty"`
}

// Migration moves a plugin's versioned configuration forward. Hooks are
// commands supplied by the plugin package and run as the user.
type Migration struct {
	From    int      `toml:"from" json:"from"`
	To      int      `toml:"to" json:"to"`
	Command []string `toml:"command" json:"command"`
}

// Startup is a command that runs while the daemon runs.
type Startup struct {
	Command []string `toml:"command" json:"command"`
	// Restart runs it again (after a pause) when it exits.
	Restart bool `toml:"restart" json:"restart,omitempty"`
}

// Action is a command run on demand.
type Action struct {
	ID      string   `toml:"id" json:"id"`
	Title   string   `toml:"title" json:"title,omitempty"`
	Command []string `toml:"command" json:"command"`
	// Async returns at once with a run ID instead of waiting for the
	// command; its outcome shows in the plugin's health and log.
	Async       bool   `toml:"async" json:"async,omitempty"`
	TimeoutText string `toml:"timeout" json:"timeout,omitempty"`
	timeout     time.Duration
}

// Event runs a command for every event of a type.
type Event struct {
	// On is an event type (worktree.created), or a prefix ending in "."
	// (agent.) for every event of that kind.
	On          string   `toml:"on" json:"on"`
	Command     []string `toml:"command" json:"command"`
	TimeoutText string   `toml:"timeout" json:"timeout,omitempty"`
	timeout     time.Duration
}

// DefaultTimeout bounds an action's or an event command's run.
const DefaultTimeout = 2 * time.Minute

// Timeout is how long the action may run.
func (a Action) Timeout() time.Duration { return a.timeout }

// Timeout is how long the event command may run.
func (e Event) Timeout() time.Duration { return e.timeout }

// Matches reports whether the handler takes events of type typ.
func (e Event) Matches(typ string) bool {
	if prefix, ok := strings.CutSuffix(e.On, "."); ok {
		return strings.HasPrefix(typ, prefix+".")
	}
	return e.On == typ
}

var (
	idPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
	windowsDrive   = regexp.MustCompile(`^[A-Za-z]:`)
	versionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?$`)
	runtimeVersion = regexp.MustCompile(`(?i)v?(\d+\.\d+\.\d+)`)
	runtimeCommand = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	eventPattern   = regexp.MustCompile(`^[a-z_]+(\.[a-z_]+)*\.?$`)
)

// ValidID reports whether s can be used as a plugin identifier.
func ValidID(s string) bool { return idPattern.MatchString(s) }

// ErrInvalidManifest wraps every problem with a manifest.
var ErrInvalidManifest = errors.New("invalid plugin manifest")

// LoadManifest reads dir's manifest.
func LoadManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return nil, err
	}
	return ParseManifest(data)
}

// ErrUnsupportedAPI is returned for a manifest written for a plugin API
// this hive does not run.
var ErrUnsupportedAPI = errors.New("unsupported plugin API")

// ParseManifest decodes and checks a manifest.
func ParseManifest(data []byte) (*Manifest, error) {
	// The API version first, leniently: a manifest for a newer API may use
	// keys this hive does not know, and deserves that answer, not "unknown
	// key".
	var head struct {
		API int    `toml:"api"`
		ID  string `toml:"id"`
	}
	if toml.Unmarshal(data, &head) == nil && head.API != 0 && !slices.Contains(SupportedAPIs, head.API) {
		return nil, fmt.Errorf("%w: plugin %s needs plugin API %d; this hive runs %s (`hive update`)", ErrUnsupportedAPI, head.ID, head.API, apiList())
	}
	var m Manifest
	dec := toml.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		if se, ok := errors.AsType[*toml.StrictMissingError](err); ok {
			return nil, fmt.Errorf("%w: %s", ErrInvalidManifest, strings.TrimSpace(se.String()))
		}
		return nil, fmt.Errorf("%w: %w", ErrInvalidManifest, err)
	}
	if err := m.check(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidManifest, err)
	}
	return &m, nil
}

func (m *Manifest) check() error {
	switch {
	case !idPattern.MatchString(m.ID):
		return fmt.Errorf("id %q: lower-case letters, digits and dashes", m.ID)
	case !versionPattern.MatchString(m.Version):
		return fmt.Errorf("version %q: want MAJOR.MINOR.PATCH", m.Version)
	case m.MinHiveVersion != "" && !versionPattern.MatchString(m.MinHiveVersion):
		return fmt.Errorf("min_hive_version %q: want MAJOR.MINOR.PATCH", m.MinHiveVersion)
	case m.MaxHiveVersion != "" && !versionPattern.MatchString(m.MaxHiveVersion):
		return fmt.Errorf("max_hive_version %q: want MAJOR.MINOR.PATCH", m.MaxHiveVersion)
	case m.MinHiveVersion != "" && m.MaxHiveVersion != "" && newer(m.MinHiveVersion, m.MaxHiveVersion):
		return errors.New("min_hive_version must not be newer than max_hive_version")
	case len(m.Startup)+len(m.Actions)+len(m.Events) == 0:
		return errors.New("nothing to run: add [[startup]], [[actions]] or [[events]]")
	}
	if m.Name == "" {
		m.Name = m.ID
	}
	if m.Test != nil {
		if len(m.Test.Command) == 0 || m.Test.Command[0] == "" {
			return errors.New("test.command: name the program to run")
		}
		if m.Test.Timeout != "" {
			if d, err := time.ParseDuration(m.Test.Timeout); err != nil || d <= 0 || d > 2*time.Hour {
				return fmt.Errorf("test.timeout %q: want a duration up to 2h", m.Test.Timeout)
			}
		}
	}
	if err := m.checkSurfaces(); err != nil {
		return err
	}
	if m.DataDirectory != "" && !safeRelativeDirectory(m.DataDirectory) {
		return fmt.Errorf("data_directory %q: want a relative path without traversal", m.DataDirectory)
	}
	if m.Configuration.Version < 0 {
		return errors.New("configuration.version must be zero or greater")
	}
	if m.Configuration.Version > 1 {
		for version := 1; version < m.Configuration.Version; version++ {
			if !migrationFrom(m.Migrations, version) {
				return fmt.Errorf("migrations: missing step from version %d to %d", version, version+1)
			}
		}
	}
	if m.Configuration.Directory != "" && !safeRelativeDirectory(m.Configuration.Directory) {
		return fmt.Errorf("configuration.directory %q: want a relative path without traversal", m.Configuration.Directory)
	}
	settings := map[string]bool{}
	for i, s := range m.Configuration.Settings {
		if !settingNamePattern.MatchString(s.Name) {
			return fmt.Errorf("configuration.settings[%d]: invalid name %q", i, s.Name)
		}
		if settings[s.Name] {
			return fmt.Errorf("configuration.settings[%d]: duplicate name %q", i, s.Name)
		}
		settings[s.Name] = true
		if s.Type != "string" && s.Type != "boolean" && s.Type != "integer" && s.Type != "number" {
			return fmt.Errorf("configuration setting %q: unsupported type %q", s.Name, s.Type)
		}
		if s.Default != nil && !validSettingValue(s.Type, s.Default) {
			return fmt.Errorf("configuration setting %q: default does not match type %q", s.Name, s.Type)
		}
	}
	if len(m.Configuration.Settings) > 0 && m.Configuration.Version == 0 {
		return errors.New("configuration.version is required when settings are declared")
	}
	migrationTargets := map[int]bool{}
	for i, migration := range m.Migrations {
		if migration.From < 0 || migration.To != migration.From+1 || len(migration.Command) == 0 || migration.Command[0] == "" {
			return fmt.Errorf("migrations[%d]: require a command and sequential from/to versions", i)
		}
		if migrationTargets[migration.From] {
			return fmt.Errorf("migrations[%d]: duplicate migration from version %d", i, migration.From)
		}
		migrationTargets[migration.From] = true
		if migration.To > m.Configuration.Version {
			return fmt.Errorf("migrations[%d]: target exceeds configuration.version", i)
		}
	}
	for version := range migrationTargets {
		if version+1 < m.Configuration.Version && !migrationTargets[version+1] {
			return fmt.Errorf("migrations: missing step from version %d to %d", version+1, version+2)
		}
	}
	for _, p := range m.Platforms {
		if p != "darwin" && p != "linux" && p != "windows" {
			return fmt.Errorf("platforms: %q (want darwin, linux or windows)", p)
		}
	}
	if err := m.checkWasm(); err != nil {
		return err
	}
	for i, r := range m.Runtimes {
		if r.Kind == RuntimeWasm {
			continue
		}
		if r.Kind != "" {
			return fmt.Errorf("runtime[%d]: kind %q (want wasm, or none for a program)", i, r.Kind)
		}
		if !runtimeCommand.MatchString(r.Command) {
			return fmt.Errorf("runtime[%d]: invalid command %q", i, r.Command)
		}
		if r.MinVersion != "" && !versionPattern.MatchString(r.MinVersion) {
			return fmt.Errorf("runtime[%d]: min_version %q: want MAJOR.MINOR.PATCH", i, r.MinVersion)
		}
	}
	for i, s := range m.Startup {
		if len(s.Command) == 0 || s.Command[0] == "" {
			return fmt.Errorf("startup[%d]: command is empty", i)
		}
	}
	seen := map[string]bool{}
	for i := range m.Actions {
		a := &m.Actions[i]
		switch {
		case !idPattern.MatchString(a.ID):
			return fmt.Errorf("actions[%d]: id %q: lower-case letters, digits and dashes", i, a.ID)
		case seen[a.ID]:
			return fmt.Errorf("actions[%d]: id %q twice", i, a.ID)
		case len(a.Command) == 0 || a.Command[0] == "":
			return fmt.Errorf("action %s: command is empty", a.ID)
		}
		seen[a.ID] = true
		t, err := timeout(a.TimeoutText)
		if err != nil {
			return fmt.Errorf("action %s: %w", a.ID, err)
		}
		a.timeout = t
		if a.Title == "" {
			a.Title = a.ID
		}
	}
	for i := range m.Events {
		e := &m.Events[i]
		switch {
		case !eventPattern.MatchString(e.On):
			return fmt.Errorf("events[%d]: on %q (want an event type such as worktree.created, or a prefix such as \"agent.\")", i, e.On)
		case len(e.Command) == 0 || e.Command[0] == "":
			return fmt.Errorf("events[%d]: command is empty", i)
		}
		t, err := timeout(e.TimeoutText)
		if err != nil {
			return fmt.Errorf("events[%d]: %w", i, err)
		}
		e.timeout = t
	}
	return nil
}

func timeout(s string) (time.Duration, error) {
	if s == "" {
		return DefaultTimeout, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 || d > time.Hour {
		return 0, fmt.Errorf("timeout %q: want a duration up to 1h, like \"30s\"", s)
	}
	return d, nil
}

// Action returns the action with id.
func (m *Manifest) Action(id string) (Action, bool) {
	i := slices.IndexFunc(m.Actions, func(a Action) bool { return a.ID == id })
	if i < 0 {
		return Action{}, false
	}
	return m.Actions[i], true
}

// Supported says why the plugin cannot run on this system with hive
// version (nil when it can).
func (m *Manifest) Supported(ctx context.Context, hiveVersion string) error {
	if len(m.Platforms) > 0 && !slices.Contains(m.Platforms, runtime.GOOS) {
		return fmt.Errorf("plugin %s runs on %s, not %s", m.ID, strings.Join(m.Platforms, ", "), runtime.GOOS)
	}
	if m.MinHiveVersion != "" && !devBuild(hiveVersion) && newer(m.MinHiveVersion, hiveVersion) {
		return fmt.Errorf("plugin %s needs hive %s or newer (this is %s; `hive update`)", m.ID, m.MinHiveVersion, hiveVersion)
	}
	if m.MaxHiveVersion != "" && !devBuild(hiveVersion) && newer(hiveVersion, m.MaxHiveVersion) {
		return fmt.Errorf("plugin %s supports hive through %s (this is %s)", m.ID, m.MaxHiveVersion, hiveVersion)
	}
	for _, r := range m.Runtimes {
		if r.Kind == RuntimeWasm {
			continue // Hive runs it
		}
		path, err := exec.LookPath(r.Command)
		if err != nil {
			return fmt.Errorf("plugin %s needs %s%s on PATH", m.ID, r.Command, minVersionLabel(r.MinVersion))
		}
		if r.MinVersion == "" {
			continue
		}
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		out, err := exec.CommandContext(checkCtx, path, "--version").Output()
		cancel()
		if err != nil {
			return fmt.Errorf("check %s version for plugin %s: %w", r.Command, m.ID, err)
		}
		version := runtimeVersion.FindStringSubmatch(string(out))
		if len(version) != 2 {
			return fmt.Errorf("cannot read %s version for plugin %s", r.Command, m.ID)
		}
		if newer(r.MinVersion, version[1]) {
			return fmt.Errorf("plugin %s needs %s %s or newer (found %s)", m.ID, r.Command, r.MinVersion, version[1])
		}
	}
	return nil
}

var settingNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func safeRelativeDirectory(s string) bool {
	if filepath.IsAbs(s) || strings.ContainsRune(s, '\\') {
		return false
	}
	if s == "." {
		return true
	}
	if strings.HasPrefix(s, "/") || windowsDrive.MatchString(s) {
		return false
	}
	for _, component := range strings.Split(s, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

func validSettingValue(typ string, value any) bool {
	switch typ {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		switch n := value.(type) {
		case int64, int:
			return true
		case float64:
			return n == float64(int64(n))
		default:
			return false
		}
	case "number":
		switch value.(type) {
		case int64, int, float64:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func minVersionLabel(version string) string {
	if version == "" {
		return ""
	}
	return " >= " + version
}

// devBuild reports a build from source ("dev", or a Go pseudo-version),
// which is taken to be new enough for any plugin.
func devBuild(v string) bool {
	return v == "" || v == "dev" || v == "(devel)" || strings.HasPrefix(strings.TrimPrefix(v, "v"), "0.0.0-")
}

// newer reports whether version a is later than b (MAJOR.MINOR.PATCH, with
// a pre-release before its release).
func newer(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := range 3 {
		if pa.n[i] != pb.n[i] {
			return pa.n[i] > pb.n[i]
		}
	}
	return pa.pre == "" && pb.pre != ""
}

type semver struct {
	n   [3]int
	pre string
}

func parts(v string) semver {
	v = strings.TrimPrefix(v, "v")
	v, _, _ = strings.Cut(v, "+")
	core, pre, _ := strings.Cut(v, "-")
	var s semver
	_, _ = fmt.Sscanf(core, "%d.%d.%d", &s.n[0], &s.n[1], &s.n[2])
	s.pre = pre
	return s
}

// Preview describes, for the user deciding whether to trust it, everything
// the plugin will run and when.
func (m *Manifest) Preview(source string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Plugin %s %s", m.ID, m.Version)
	if m.Name != m.ID {
		fmt.Fprintf(&b, " (%s)", m.Name)
	}
	b.WriteString("\n")
	if m.Description != "" {
		fmt.Fprintf(&b, "  %s\n", m.Description)
	}
	if source != "" {
		fmt.Fprintf(&b, "  from %s\n", source)
	}
	if m.MinHiveVersion != "" || m.MaxHiveVersion != "" {
		minimum, maximum := m.MinHiveVersion, m.MaxHiveVersion
		if minimum == "" {
			minimum = "any"
		}
		if maximum == "" {
			maximum = "any"
		}
		fmt.Fprintf(&b, "  hive compatibility: %s through %s\n", minimum, maximum)
	}
	if len(m.Configuration.Settings) > 0 {
		fmt.Fprintf(&b, "  configuration schema %d (%d settings) in a separate persistent config directory\n", m.Configuration.Version, len(m.Configuration.Settings))
		for _, setting := range m.Configuration.Settings {
			fmt.Fprintf(&b, "    %s (%s)", setting.Name, setting.Type)
			if setting.Required {
				b.WriteString(", required")
			}
			if setting.Default != nil {
				fmt.Fprintf(&b, ", default %v", setting.Default)
			}
			if setting.Description != "" {
				fmt.Fprintf(&b, ": %s", setting.Description)
			}
			b.WriteByte('\n')
		}
	}
	if len(m.Migrations) > 0 {
		fmt.Fprintf(&b, "  configuration/data migrations: %d command(s)\n", len(m.Migrations))
	}
	if m.Legacy() {
		b.WriteString("  plugin API: unversioned (it declares no permissions, so it holds them all)\n")
	} else {
		fmt.Fprintf(&b, "  plugin API: %d\n", m.API)
		if len(m.Permissions.Required) == 0 {
			b.WriteString("  permissions required: none\n")
		}
		for _, c := range m.Permissions.Required {
			fmt.Fprintf(&b, "  requires %s: %s%s\n", c, describeCapability(c), reasonSuffix(m.Permissions.Reasons[c]))
		}
		for _, c := range m.Permissions.Optional {
			fmt.Fprintf(&b, "  may ask for %s: %s%s\n", c, describeCapability(c), reasonSuffix(m.Permissions.Reasons[c]))
		}
	}
	switch {
	case m.Wasm():
		b.WriteString("  runs as WebAssembly inside Hive: no network, no programs, only its own data and settings\n")
	case m.API >= 2 && m.Isolation == IsolationNone:
		fmt.Fprintf(&b, "  runs UNCONFINED, outside the sandbox: %s\n", m.IsolationReason)
	case m.API >= 2:
		b.WriteString("  runs sandboxed: it reads only its own files and the system's, and writes, connects and runs programs only as granted\n")
	case m.Isolation == "strict":
		b.WriteString("  can run isolated (strict): no network or outside writes unless granted\n")
	}
	if hosts := m.Permissions.Network.Hosts; len(hosts) > 0 {
		fmt.Fprintf(&b, "  network limited to: %s\n", strings.Join(hosts, ", "))
	}
	if exec := m.Permissions.Process.Exec; len(exec) > 0 {
		fmt.Fprintf(&b, "  programs limited to: %s\n", strings.Join(exec, ", "))
	}
	c := m.Contributes
	for _, card := range c.Cards {
		fmt.Fprintf(&b, "  sidebar card %q (%s)\n", card.Title, card.Scope)
	}
	for _, s := range c.Status {
		fmt.Fprintf(&b, "  status indicator %q\n", s.Title)
	}
	for _, a := range c.Actions {
		fmt.Fprintf(&b, "  action %q on %s\n", a.Title, strings.Join(a.Contexts, ", "))
	}
	for _, p := range c.Palette {
		fmt.Fprintf(&b, "  palette entry %q\n", p.Title)
	}
	for _, l := range c.Links {
		fmt.Fprintf(&b, "  link %q: %s\n", l.Title, l.URL)
	}
	b.WriteString("  It runs as you, with your files and your Hive sessions:\n")
	for _, s := range m.Startup {
		when := "while hive runs"
		if s.Restart {
			when += ", restarted when it stops"
		}
		fmt.Fprintf(&b, "    %s: %s\n", when, strings.Join(s.Command, " "))
	}
	for _, e := range m.Events {
		fmt.Fprintf(&b, "    on %s: %s\n", e.On, strings.Join(e.Command, " "))
	}
	for _, a := range m.Actions {
		fmt.Fprintf(&b, "    action %s (%s): %s\n", a.ID, a.Title, strings.Join(a.Command, " "))
	}
	return b.String()
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return " (" + reason + ")"
}
