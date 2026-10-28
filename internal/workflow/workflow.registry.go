package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// fileStamp tells whether a workflow file changed since it was read.
type fileStamp struct {
	mod  time.Time
	size int64
}

// source is a workflow file to read, and whether it is the person's own.
type sourceFile struct {
	path string
	own  bool
}

// sources lists the workflow files: the person's own first, then the
// shared ones in [workflows] paths, each directory's *.toml in name order.
func (s *Service) sources() []sourceFile {
	var out []sourceFile
	seen := map[string]bool{}
	add := func(path string, own bool) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		if !seen[path] {
			seen[path] = true
			out = append(out, sourceFile{path: path, own: own})
		}
	}
	dir := func(d string, own bool) {
		matches, _ := filepath.Glob(filepath.Join(d, "*.toml"))
		slices.Sort(matches)
		for _, m := range matches {
			add(m, own)
		}
	}
	if s.cfg.OwnDir != "" {
		dir(s.cfg.OwnDir, true)
	}
	for _, p := range s.cfg.Paths {
		fi, err := os.Stat(p)
		switch {
		case err != nil:
			s.log.Debug("a workflow path is missing", "path", p, "err", err)
		case fi.IsDir():
			dir(p, false)
		default:
			add(p, false)
		}
	}
	return out
}

// reload reads the workflow files again where they changed, and reports
// the names whose definitions changed.
func (s *Service) reload(ctx context.Context) []string {
	files := s.sources()
	s.mu.Lock()
	old := s.defs
	byPath := map[string]*Workflow{}
	for _, w := range old {
		byPath[w.Path] = w
	}
	stamps := maps.Clone(s.files)
	s.mu.Unlock()

	defs := map[string]*Workflow{}
	newStamps := map[string]fileStamp{}
	for _, f := range files {
		if ctx.Err() != nil {
			return nil
		}
		fi, err := os.Stat(f.path)
		if err != nil {
			continue
		}
		stamp := fileStamp{mod: fi.ModTime(), size: fi.Size()}
		newStamps[f.path] = stamp
		w := byPath[f.path]
		if w == nil || stamps[f.path] != stamp {
			w = readWorkflow(f.path)
		} else {
			c := *w
			w = &c
		}
		w.Own = f.own
		if prev := defs[w.Name]; prev != nil {
			w.Error = fmt.Sprintf("another workflow is called %s (%s)", w.Name, prev.Path)
			w.Name = NameFromSource(f.path) + "@" + shortDigest(w.Digest)
		}
		defs[w.Name] = w
	}
	var changed []string
	for name, w := range defs {
		if o := old[name]; o == nil || o.Digest != w.Digest || o.Path != w.Path || o.Error != w.Error || o.Own != w.Own {
			changed = append(changed, name)
		}
	}
	for name := range old {
		if defs[name] == nil {
			changed = append(changed, name)
		}
	}
	slices.Sort(changed)
	s.mu.Lock()
	s.defs, s.files = defs, newStamps
	s.mu.Unlock()
	if len(changed) > 0 {
		s.log.Info("workflows read", "changed", strings.Join(changed, ", "), "total", len(defs))
		s.ports.Events.Publish(EventChanged, map[string][]string{"names": changed})
		s.triggers.definitionsChanged()
	}
	return changed
}

func shortDigest(d string) string {
	if len(d) > 8 {
		return d[:8]
	}
	return d
}

// readWorkflow reads and parses one file; a file that does not parse is
// kept with its error, under its file's name.
func readWorkflow(path string) *Workflow {
	w := &Workflow{Path: path, Name: NameFromSource(path)}
	f, err := os.Open(path)
	if err != nil {
		w.Error = err.Error()
		return w
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		w.Error = err.Error()
		return w
	}
	sum := sha256.Sum256(data)
	w.Digest, w.source = hex.EncodeToString(sum[:]), data
	spec, err := Parse(data, filepath.Base(path))
	if err != nil {
		w.Error = err.Error()
		if !ValidName(w.Name) {
			w.Name = "file-" + shortDigest(w.Digest)
		}
		return w
	}
	w.Spec = &spec
	w.Name, w.Description, w.Inputs, w.Steps = spec.Name, spec.Description, spec.Inputs, len(spec.Steps)
	for _, t := range spec.Triggers {
		w.Triggers = append(w.Triggers, describe(t))
	}
	return w
}

// viewLocked is a definition as the API shows it: trust, its latest run
// and its next scheduled time; full adds its spec and text.
func (s *Service) viewLocked(w *Workflow, full bool) Workflow {
	v := *w
	v.Trusted = w.Digest != "" && s.trust[w.Path] == w.Digest
	if !full {
		v.Spec, v.Text = nil, ""
	} else {
		v.Text = string(w.source)
	}
	for i := len(s.order) - 1; i >= 0; i-- {
		if s.order[i].Workflow == w.Name {
			sum := s.order[i].Summary()
			v.Last = &sum
			break
		}
	}
	v.Next = s.triggers.nextLocked(w.Name)
	v.Problems = s.stepProblems(w.Spec)
	return v
}

// stepProblems checks a workflow's plugin steps against the plugins
// installed now.
func (s *Service) stepProblems(spec *Spec) []string {
	if spec == nil || s.ports.Steps == nil {
		return nil
	}
	var out []string
	for _, st := range spec.Steps {
		if st.Kind() != KindStep {
			continue
		}
		plugin, step, _ := strings.Cut(st.Uses, "/")
		if err := s.ports.Steps.Check(plugin, step, st.With); err != nil {
			out = append(out, st.ID+": "+err.Error())
		}
	}
	return out
}

// Workflows lists the workflow files, by name.
func (s *Service) Workflows(ctx context.Context) []Workflow {
	if s.ready(ctx) != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Workflow, 0, len(s.defs))
	for _, name := range sortedKeys(s.defs) {
		out = append(out, s.viewLocked(s.defs[name], false))
	}
	return out
}

// Workflow returns one workflow, with its spec and text.
func (s *Service) Workflow(ctx context.Context, name string) (Workflow, error) {
	if err := s.ready(ctx); err != nil {
		return Workflow{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.defs[name]
	if w == nil {
		return Workflow{}, fmt.Errorf("%w: no workflow %q (hive workflow list)", ErrNotFound, name)
	}
	return s.viewLocked(w, true), nil
}

// Reload reads the workflow files again.
func (s *Service) Reload(ctx context.Context) []Workflow {
	if s.ready(ctx) != nil {
		return nil
	}
	s.reload(ctx)
	return s.Workflows(ctx)
}

// Trust lets a shared workflow's triggers fire while its file keeps its
// current text, or with revoke stops them.
func (s *Service) Trust(ctx context.Context, name string, revoke bool) (Workflow, error) {
	if err := s.ready(ctx); err != nil {
		return Workflow{}, err
	}
	s.reload(ctx) // trust what the file says now
	s.mu.Lock()
	w := s.defs[name]
	if w == nil {
		s.mu.Unlock()
		return Workflow{}, fmt.Errorf("%w: no workflow %q (hive workflow list)", ErrNotFound, name)
	}
	if w.Error != "" && !revoke {
		s.mu.Unlock()
		return Workflow{}, fmt.Errorf("%w: %s does not parse: %s", ErrState, name, w.Error)
	}
	trust := maps.Clone(s.trust)
	if revoke {
		delete(trust, w.Path)
	} else {
		trust[w.Path] = w.Digest
	}
	s.mu.Unlock()
	if err := s.store.SaveTrust(trust); err != nil {
		return Workflow{}, err
	}
	s.mu.Lock()
	s.trust = trust
	v := s.viewLocked(w, false)
	s.mu.Unlock()
	if revoke {
		s.log.Info("workflow trust revoked", "workflow", name, "path", w.Path)
	} else {
		s.log.Info("workflow trusted", "workflow", name, "path", w.Path, "digest", w.Digest)
	}
	s.triggers.definitionsChanged() // its triggers arm, or stop
	s.ports.Events.Publish(EventChanged, map[string][]string{"names": {name}})
	return v, nil
}

// firesLocked reports whether a definition's triggers may fire now.
func (s *Service) firesLocked(w *Workflow) bool {
	return w.Error == "" && w.Spec != nil && (w.Own || s.trust[w.Path] == w.Digest)
}

// Validate parses the text of a workflow file.
func (s *Service) Validate(text, source string) (Spec, error) {
	spec, err := Parse([]byte(text), cmpStr(source, "workflow"))
	if err != nil {
		return spec, err
	}
	if p := s.stepProblems(&spec); len(p) > 0 {
		return Spec{}, invalid("%s: %s", cmpStr(source, "workflow"), strings.Join(p, "; "))
	}
	return spec, nil
}

// defaultDir is where a run of a workflow file runs when nothing else
// says: the repository its .hive/workflows directory is in.
func defaultDir(path string) string {
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "workflows" && filepath.Base(filepath.Dir(dir)) == ".hive" {
		return filepath.Dir(filepath.Dir(dir))
	}
	return ""
}
