package agent

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/admirable-oss/hive/internal/vt"
)

// Manifest describes one kind of coding agent: how to recognise its
// process, how to start it, and the rules that read its state from its
// screen. Manifests are TOML files (distribution/agent-detection/*.toml,
// overridable per user); see that directory's README for the format.
type Manifest struct {
	ID   string `toml:"id" json:"id"`
	Name string `toml:"name" json:"name"`
	// Priority breaks ties when several manifests recognise a process.
	Priority int `toml:"priority" json:"priority,omitempty"`
	// Synthetic marks manifests whose fixtures were written from the
	// agent's documentation rather than recorded from it.
	Synthetic bool `toml:"synthetic" json:"synthetic,omitempty"`

	Detect Detect `toml:"detect" json:"detect"`
	Start  Start  `toml:"start" json:"start"`
	// Activity, when true, treats a screen that keeps changing without any
	// rule matching as working, and a quiet one as idle. Agents whose
	// rules reliably mark work (a spinner, "esc to interrupt") turn it
	// off, so typing in their prompt box is not mistaken for work.
	Activity bool   `toml:"activity" json:"activity"`
	Rules    []Rule `toml:"rules" json:"rules"`

	// Source is the file the manifest came from; set on load.
	Source string `toml:"-" json:"source,omitempty"`
}

// Detect recognises an agent's process from its arguments.
type Detect struct {
	// Process lists program names, matched against the base names of
	// argv[0] and, for interpreted programs (node, python, bun), argv[1].
	Process []string `toml:"process" json:"process"`
	// ArgsRegex, when set, must also match the arguments joined by spaces.
	ArgsRegex string `toml:"args_regex" json:"args_regex,omitempty"`

	argsRe *regexp.Regexp
}

// Start is how `hive agent start` launches the agent.
type Start struct {
	Command []string `toml:"command" json:"command,omitempty"`
	// Submit is the key that sends a prompt after it is pasted (default
	// Enter).
	Submit string `toml:"submit" json:"submit,omitempty"`
	// SubmitDelayMS separates the pasted prompt from the submit key
	// (default 150).
	SubmitDelayMS int `toml:"submit_delay_ms" json:"submit_delay_ms,omitempty"`
}

// Rule maps what a screen shows to a state.
type Rule struct {
	State State `toml:"state" json:"state"`
	// Priority orders rules: the matching rule with the highest wins.
	Priority int `toml:"priority" json:"priority"`
	// Region is the part of the screen the rule reads: screen (default),
	// bottom:N, top:N, lines:A-B (0-based, inclusive) or title.
	Region      string  `toml:"region" json:"region,omitempty"`
	Match       Matcher `toml:"match" json:"match"`
	Description string  `toml:"description" json:"description,omitempty"`
}

// Matcher tests a region's text. Exactly one of Contains, Regex,
// LineRegex, Any, All or Not is set; IgnoreCase applies to the text tests.
type Matcher struct {
	Contains   string    `toml:"contains" json:"contains,omitempty"`
	Regex      string    `toml:"regex" json:"regex,omitempty"`           // anywhere in the region (lines joined by \n)
	LineRegex  string    `toml:"line_regex" json:"line_regex,omitempty"` // some single line
	Any        []Matcher `toml:"any" json:"any,omitempty"`
	All        []Matcher `toml:"all" json:"all,omitempty"`
	Not        *Matcher  `toml:"not" json:"not,omitempty"`
	IgnoreCase bool      `toml:"ignore_case" json:"ignore_case,omitempty"`

	re *regexp.Regexp
}

// ParseManifest reads and checks a manifest.
func ParseManifest(data []byte, source string) (*Manifest, error) {
	var m Manifest
	dec := toml.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		var se *toml.StrictMissingError
		if errors.As(err, &se) {
			return nil, fmt.Errorf("%s: %s", source, strings.TrimSpace(se.String()))
		}
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	m.Source = source
	if err := m.compile(); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return &m, nil
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func (m *Manifest) compile() error {
	switch {
	case !idPattern.MatchString(m.ID):
		return fmt.Errorf("id %q: want lower-case letters, digits and dashes", m.ID)
	case m.Name == "":
		return errors.New("name is required")
	case len(m.Detect.Process) == 0:
		return errors.New("detect.process needs at least one program name")
	}
	if m.Detect.ArgsRegex != "" {
		re, err := regexp.Compile(m.Detect.ArgsRegex)
		if err != nil {
			return fmt.Errorf("detect.args_regex: %w", err)
		}
		m.Detect.argsRe = re
	}
	for i := range m.Rules {
		r := &m.Rules[i]
		if !r.State.detectable() {
			return fmt.Errorf("rules[%d]: state %q (want working, blocked, idle or done)", i, r.State)
		}
		if _, err := parseRegion(r.Region); err != nil {
			return fmt.Errorf("rules[%d]: %w", i, err)
		}
		if err := r.Match.compile(); err != nil {
			return fmt.Errorf("rules[%d].match: %w", i, err)
		}
	}
	// Highest priority first; stable, so equal priorities keep file order.
	slices.SortStableFunc(m.Rules, func(a, b Rule) int { return b.Priority - a.Priority })
	return nil
}

func (mt *Matcher) compile() error {
	set := 0
	for _, b := range []bool{mt.Contains != "", mt.Regex != "", mt.LineRegex != "", len(mt.Any) > 0, len(mt.All) > 0, mt.Not != nil} {
		if b {
			set++
		}
	}
	if set != 1 {
		return errors.New("a matcher needs exactly one of contains, regex, line_regex, any, all or not")
	}
	pattern := mt.Regex
	if mt.LineRegex != "" {
		pattern = mt.LineRegex
	}
	if pattern != "" {
		if mt.IgnoreCase {
			pattern = "(?i)" + pattern
		}
		if mt.LineRegex == "" {
			pattern = "(?m)" + pattern // ^ and $ match at line breaks
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return err
		}
		mt.re = re
	}
	for i := range mt.Any {
		if err := mt.Any[i].compile(); err != nil {
			return fmt.Errorf("any[%d]: %w", i, err)
		}
	}
	for i := range mt.All {
		if err := mt.All[i].compile(); err != nil {
			return fmt.Errorf("all[%d]: %w", i, err)
		}
	}
	if mt.Not != nil {
		if err := mt.Not.compile(); err != nil {
			return fmt.Errorf("not: %w", err)
		}
	}
	return nil
}

// Matches reports whether the manifest recognises a process with args.
func (m *Manifest) Matches(args []string) bool {
	if len(args) == 0 {
		return false
	}
	names := []string{filepath.Base(args[0])}
	if len(args) > 1 && isInterpreter(names[0]) {
		names = append(names, strings.TrimSuffix(filepath.Base(args[1]), filepath.Ext(args[1])), filepath.Base(args[1]))
	}
	found := slices.ContainsFunc(m.Detect.Process, func(p string) bool { return slices.Contains(names, p) })
	if !found {
		return false
	}
	if m.Detect.argsRe != nil {
		return m.Detect.argsRe.MatchString(strings.Join(args, " "))
	}
	return true
}

// isInterpreter reports whether a program runs a script given as argv[1].
func isInterpreter(name string) bool {
	name = strings.TrimRight(name, "0123456789.")
	switch name {
	case "node", "nodejs", "bun", "deno", "python", "python3", "ruby",
		"sh", "bash", "zsh", "dash": // shell-script launchers (version managers' shims)
		return true
	}
	return false
}

// region is a parsed rule region.
type region struct {
	kind    string // screen, bottom, top, lines, title
	n, a, b int
}

func parseRegion(s string) (region, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "" || s == "screen":
		return region{kind: "screen"}, nil
	case s == "title":
		return region{kind: "title"}, nil
	case strings.HasPrefix(s, "bottom:"), strings.HasPrefix(s, "top:"):
		kind, num, _ := strings.Cut(s, ":")
		n, err := strconv.Atoi(num)
		if err != nil || n < 1 {
			return region{}, fmt.Errorf("region %q: want %s:N with N ≥ 1", s, kind)
		}
		return region{kind: kind, n: n}, nil
	case strings.HasPrefix(s, "lines:"):
		from, to, ok := strings.Cut(strings.TrimPrefix(s, "lines:"), "-")
		a, err1 := strconv.Atoi(from)
		b, err2 := strconv.Atoi(to)
		if !ok || err1 != nil || err2 != nil || a < 0 || b < a {
			return region{}, fmt.Errorf("region %q: want lines:A-B with 0 ≤ A ≤ B", s)
		}
		return region{kind: "lines", a: a, b: b}, nil
	}
	return region{}, fmt.Errorf("region %q: want screen, bottom:N, top:N, lines:A-B or title", s)
}

// rows returns the screen rows [from, to) the region covers; title covers
// none.
func (r region) rows(height int) (int, int) {
	switch r.kind {
	case "bottom":
		return max(height-r.n, 0), height
	case "top":
		return 0, min(r.n, height)
	case "lines":
		return min(r.a, height), min(r.b+1, height)
	case "title":
		return 0, 0
	}
	return 0, height
}

// text is the region's text: its lines without trailing blanks, or the
// title.
func (r region) text(s *vt.Screen) []string {
	if r.kind == "title" {
		return []string{s.Title}
	}
	from, to := r.rows(len(s.Lines))
	out := make([]string, 0, to-from)
	for y := from; y < to; y++ {
		out = append(out, vt.LineText(s.Lines[y]))
	}
	return out
}

// match tests lines and returns whether they match, with the text that
// did for explanations.
func (mt *Matcher) match(lines []string) (bool, string) {
	switch {
	case mt.Contains != "":
		for _, l := range lines {
			hay, needle := l, mt.Contains
			if mt.IgnoreCase {
				hay, needle = strings.ToLower(hay), strings.ToLower(needle)
			}
			if strings.Contains(hay, needle) {
				return true, strings.TrimSpace(l)
			}
		}
		return false, ""
	case mt.LineRegex != "":
		for _, l := range lines {
			if mt.re.MatchString(l) {
				return true, strings.TrimSpace(l)
			}
		}
		return false, ""
	case mt.Regex != "":
		joined := strings.Join(lines, "\n")
		if loc := mt.re.FindStringIndex(joined); loc != nil {
			return true, strings.TrimSpace(joined[loc[0]:loc[1]])
		}
		return false, ""
	case len(mt.Any) > 0:
		for i := range mt.Any {
			if ok, why := mt.Any[i].match(lines); ok {
				return true, why
			}
		}
		return false, ""
	case len(mt.All) > 0:
		var whys []string
		for i := range mt.All {
			ok, why := mt.All[i].match(lines)
			if !ok {
				return false, ""
			}
			whys = append(whys, why)
		}
		return true, strings.Join(whys, " … ")
	case mt.Not != nil:
		ok, _ := mt.Not.match(lines)
		return !ok, ""
	}
	return false, ""
}

// Verdict is what a manifest's rules read from a screen.
type Verdict struct {
	State State `json:"state"`
	// Rule is the index of the matching rule in priority order, -1 for
	// none; Text is what it matched.
	Rule        int    `json:"rule"`
	Description string `json:"description,omitempty"`
	Region      string `json:"region,omitempty"`
	Text        string `json:"text,omitempty"`
}

// Classify applies the rules to a screen: the first match in priority
// order wins.
func (m *Manifest) Classify(s *vt.Screen) Verdict {
	for i := range m.Rules {
		r := &m.Rules[i]
		reg, _ := parseRegion(r.Region)
		if ok, why := r.Match.match(reg.text(s)); ok {
			return Verdict{State: r.State, Rule: i, Description: r.Description, Region: regionName(r.Region), Text: why}
		}
	}
	return Verdict{State: StateUnknown, Rule: -1}
}

func regionName(r string) string {
	if r == "" {
		return "screen"
	}
	return r
}

// touches reports whether any rule reads one of the changed rows, or the
// title when it changed; unchanged regions keep their last verdict.
func (m *Manifest) touches(rows map[int]bool, height int, titleChanged bool) bool {
	for i := range m.Rules {
		reg, _ := parseRegion(m.Rules[i].Region)
		if reg.kind == "title" {
			if titleChanged {
				return true
			}
			continue
		}
		from, to := reg.rows(height)
		for y := range rows {
			if y >= from && y < to {
				return true
			}
		}
	}
	return false
}
