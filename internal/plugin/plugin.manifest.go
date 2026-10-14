// Package plugin extends Hive with programs it runs for the user: a plugin
// is a directory with a hive-plugin.toml manifest. The daemon runs its
// [[startup]] commands while it runs, its [[events]] commands when a
// matching event happens, and its [[actions]] when asked (from the CLI, a
// key binding or the UI). Plugins talk back to Hive through the hive CLI
// ($HIVE_BIN), like any agent in a pane: report-metadata for sidebar
// tokens, agent start for new agents, and so on.
//
// Every command gets HIVE_PLUGIN_ID, HIVE_PLUGIN_DIR, HIVE_PLUGIN_DATA (a
// directory it may write) and HIVE_PLUGIN_VERSION, plus a context JSON
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
	ID          string `toml:"id" json:"id"`
	Name        string `toml:"name" json:"name,omitempty"`
	Version     string `toml:"version" json:"version"`
	Description string `toml:"description" json:"description,omitempty"`
	// MinHiveVersion is the oldest hive the plugin works with.
	MinHiveVersion string `toml:"min_hive_version" json:"min_hive_version,omitempty"`
	// Platforms lists the systems it runs on (darwin, linux); empty: all.
	Platforms []string `toml:"platforms" json:"platforms,omitempty"`
	// Runtimes names executables needed to run the published plugin artifact.
	// Plugin releases should include built output so installation never has to
	// download packages or invoke a compiler.
	Runtimes []Runtime `toml:"runtime" json:"runtime,omitempty"`

	Startup []Startup `toml:"startup" json:"startup,omitempty"`
	Actions []Action  `toml:"actions" json:"actions,omitempty"`
	Events  []Event   `toml:"events" json:"events,omitempty"`
}

// Runtime is an executable requirement checked before installation and when
// loading an installed plugin.
type Runtime struct {
	Command    string `toml:"command" json:"command"`
	MinVersion string `toml:"min_version" json:"min_version,omitempty"`
}

// Startup is a command that runs while the daemon runs.
type Startup struct {
	Command []string `toml:"command" json:"command"`
	// Restart runs it again (after a pause) when it exits.
	Restart bool `toml:"restart" json:"restart,omitempty"`
}

// Action is a command run on demand.
type Action struct {
	ID          string   `toml:"id" json:"id"`
	Title       string   `toml:"title" json:"title,omitempty"`
	Command     []string `toml:"command" json:"command"`
	TimeoutText string   `toml:"timeout" json:"timeout,omitempty"`
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

// ParseManifest decodes and checks a manifest.
func ParseManifest(data []byte) (*Manifest, error) {
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
	case len(m.Startup)+len(m.Actions)+len(m.Events) == 0:
		return errors.New("nothing to run: add [[startup]], [[actions]] or [[events]]")
	}
	if m.Name == "" {
		m.Name = m.ID
	}
	for _, p := range m.Platforms {
		if p != "darwin" && p != "linux" {
			return fmt.Errorf("platforms: %q (want darwin or linux)", p)
		}
	}
	for i, r := range m.Runtimes {
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
func (m *Manifest) Supported(hiveVersion string) error {
	if len(m.Platforms) > 0 && !slices.Contains(m.Platforms, runtime.GOOS) {
		return fmt.Errorf("plugin %s runs on %s, not %s", m.ID, strings.Join(m.Platforms, ", "), runtime.GOOS)
	}
	if m.MinHiveVersion != "" && !devBuild(hiveVersion) && newer(m.MinHiveVersion, hiveVersion) {
		return fmt.Errorf("plugin %s needs hive %s or newer (this is %s; `hive update`)", m.ID, m.MinHiveVersion, hiveVersion)
	}
	for _, r := range m.Runtimes {
		path, err := exec.LookPath(r.Command)
		if err != nil {
			return fmt.Errorf("plugin %s needs %s%s on PATH", m.ID, r.Command, minVersionLabel(r.MinVersion))
		}
		if r.MinVersion == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		out, err := exec.CommandContext(ctx, path, "--version").Output()
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
