package workflow

import (
	"context"
	"encoding/json"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// triggers fires workflows: on Hive events (Notify), on schedules, on git
// pushes and new branches, and on webhooks (Fire).
type triggers struct {
	s *Service

	mu    sync.Mutex
	state TriggerState
	next  map[string]time.Time // schedule and every triggers, by key
	// nextOf is the schedule each next was computed for, so an edited one
	// is computed again.
	nextOf  map[string]string
	events  []eventTrigger // the event triggers that may fire
	changed chan struct{}  // definitions changed
	gitPoke chan struct{}  // a repository's git state changed
}

// eventTrigger is an event trigger of a workflow that may fire.
type eventTrigger struct {
	workflow string
	index    int
	spec     TriggerSpec
}

func newTriggers(s *Service) *triggers {
	return &triggers{
		s: s, next: map[string]time.Time{}, nextOf: map[string]string{},
		changed: make(chan struct{}, 1), gitPoke: make(chan struct{}, 1),
		state: TriggerState{Refs: map[string]map[string]string{}, Every: map[string]time.Time{}},
	}
}

func triggerKey(workflow string, i int) string { return workflow + "#" + strconv.Itoa(i) }

func (t *triggers) load(st TriggerState) {
	t.mu.Lock()
	t.state = st
	t.mu.Unlock()
}

// definitionsChanged rebuilds what may fire before it returns, so an
// event right after a reload (or a trust) finds the triggers armed, and
// has the run loop look at its schedules again.
func (t *triggers) definitionsChanged() {
	t.refresh(t.s.cfg.Now())
	signal(t.changed)
}

// GitChanged says a repository's git state changed; watched repositories
// are read again soon.
func (s *Service) GitChanged() { signal(s.triggers.gitPoke) }

// run fires schedules, reads watched repositories and workflow files,
// until ctx ends.
func (t *triggers) run(ctx context.Context) {
	t.refresh(t.s.cfg.Now())
	t.scanGit(ctx)
	poll := time.NewTicker(t.s.cfg.Interval)
	defer poll.Stop()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	var debounce <-chan time.Time
	for {
		timer.Reset(t.untilNext())
		select {
		case <-ctx.Done():
			return
		case <-t.changed:
			t.refresh(t.s.cfg.Now())
		case <-poll.C:
			t.s.reload(ctx)
			t.scanGit(ctx)
		case <-t.gitPoke:
			if debounce == nil {
				debounce = time.After(500 * time.Millisecond)
			}
		case <-debounce:
			debounce = nil
			t.scanGit(ctx)
		case <-timer.C:
			t.fireDue(ctx, t.s.cfg.Now())
		}
	}
}

// refresh rebuilds what may fire from the definitions: the event
// triggers, and the next time of each schedule.
func (t *triggers) refresh(now time.Time) {
	type sched struct {
		key  string
		spec TriggerSpec
	}
	var (
		events []eventTrigger
		scheds []sched
	)
	t.s.mu.Lock()
	for _, name := range sortedKeys(t.s.defs) {
		w := t.s.defs[name]
		if !t.s.firesLocked(w) {
			continue
		}
		for i, tr := range w.Spec.Triggers {
			switch tr.Type() {
			case TriggerEvent:
				events = append(events, eventTrigger{workflow: name, index: i, spec: tr})
			case TriggerSchedule, TriggerEvery:
				scheds = append(scheds, sched{key: triggerKey(name, i), spec: tr})
			}
		}
	}
	t.s.mu.Unlock()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = events
	next, nextOf := map[string]time.Time{}, map[string]string{}
	for _, sc := range scheds {
		what := sc.spec.Schedule + "|" + sc.spec.Every().String()
		nextOf[sc.key] = what
		if n, ok := t.next[sc.key]; ok && t.nextOf[sc.key] == what {
			next[sc.key] = n // unchanged: keep its time
			continue
		}
		next[sc.key] = t.nextTime(sc.key, sc.spec, now)
	}
	t.next, t.nextOf = next, nextOf
}

// nextTime is when a schedule next fires after now. An every trigger
// counts from its last run (a restart does not restart its interval); one
// that came due while no daemon ran waits a whole interval.
func (t *triggers) nextTime(key string, spec TriggerSpec, now time.Time) time.Time {
	if spec.Type() == TriggerEvery {
		if last, ok := t.state.Every[key]; ok {
			if n := last.Add(spec.Every()); n.After(now) {
				return n
			}
		}
		return now.Add(spec.Every())
	}
	sch, err := ParseSchedule(spec.Schedule)
	if err != nil {
		return time.Time{}
	}
	return sch.Next(now)
}

func (t *triggers) untilNext() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	wait := time.Hour
	for _, n := range t.next {
		if n.IsZero() {
			continue
		}
		wait = min(wait, max(time.Until(n), 10*time.Millisecond))
	}
	return wait
}

// nextLocked is when a workflow's next schedule fires (s.mu is held).
func (t *triggers) nextLocked(workflow string) time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	var best time.Time
	for k, n := range t.next {
		if strings.HasPrefix(k, workflow+"#") && !n.IsZero() && (best.IsZero() || n.Before(best)) {
			best = n
		}
	}
	return best
}

// fireDue fires the schedules whose time has come.
func (t *triggers) fireDue(ctx context.Context, now time.Time) {
	type due struct {
		workflow string
		index    int
		at       time.Time
	}
	var fire []due
	t.mu.Lock()
	for _, key := range sortedKeys(t.next) {
		n := t.next[key]
		if n.IsZero() || now.Before(n) {
			continue
		}
		name, idx, _ := strings.Cut(key, "#")
		i, _ := strconv.Atoi(idx)
		fire = append(fire, due{workflow: name, index: i, at: n})
		delete(t.next, key) // refresh recomputes it
		t.state.Every[key] = now
	}
	st := t.cloneStateLocked()
	t.mu.Unlock()
	if len(fire) == 0 {
		return
	}
	if err := t.s.store.SaveState(st); err != nil {
		t.s.log.Warn("saving workflow trigger state", "err", err)
	}
	for _, d := range fire {
		typ := TriggerSchedule
		t.s.mu.Lock()
		if w := t.s.defs[d.workflow]; w != nil && w.Spec != nil && d.index < len(w.Spec.Triggers) {
			typ = w.Spec.Triggers[d.index].Type()
		}
		t.s.mu.Unlock()
		t.s.fire(ctx, d.workflow, d.index, Trigger{Type: typ, Index: d.index, Time: now, Data: map[string]any{
			"time": d.at.Format(time.RFC3339),
		}}, "")
	}
	t.refresh(now)
}

func (t *triggers) cloneStateLocked() TriggerState {
	st := TriggerState{Refs: make(map[string]map[string]string, len(t.state.Refs)), Every: maps.Clone(t.state.Every)}
	for k, v := range t.state.Refs {
		st.Refs[k] = maps.Clone(v)
	}
	return st
}

// --- Events ---

// Notify is a Hive event: the workflows whose event triggers match it
// fire. A run never triggers its own workflow.
func (s *Service) Notify(ctx context.Context, typ string, seq uint64, data json.RawMessage) {
	t := s.triggers
	t.mu.Lock()
	var hits []eventTrigger
	for _, et := range t.events {
		if eventMatches(et.spec.Event, typ) {
			hits = append(hits, et)
		}
	}
	t.mu.Unlock()
	if len(hits) == 0 {
		return
	}
	var decoded any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &decoded); err != nil {
			return
		}
	}
	for _, et := range hits {
		if !fieldsMatch(et.spec.Match, decoded) {
			continue
		}
		if m, ok := decoded.(map[string]any); ok && strings.HasPrefix(typ, "workflow.") && m["workflow"] == et.workflow {
			continue // its own run
		}
		dir := ""
		if m, ok := decoded.(map[string]any); ok {
			if env, ok := m["environment_id"].(string); ok && env != "" && s.ports.Workspace != nil {
				dir, _ = s.ports.Workspace.EnvironmentDir(ctx, env)
			}
		}
		s.fire(ctx, et.workflow, et.index, Trigger{
			Type: TriggerEvent, Index: et.index, Time: s.cfg.Now(),
			Data: map[string]any{"event": typ, "seq": float64(seq), "data": decoded},
		}, dir)
	}
}

// eventMatches reports whether an event of type typ matches want: the
// same type, or want a prefix ending in ".".
func eventMatches(want, typ string) bool {
	if strings.HasSuffix(want, ".") {
		return strings.HasPrefix(typ, want)
	}
	return want == typ
}

// fieldsMatch reports whether every dotted path in match has, in data, a
// value its pattern matches.
func fieldsMatch(match map[string]string, data any) bool {
	for k, pattern := range match {
		v := data
		for part := range strings.SplitSeq(k, ".") {
			m, ok := v.(map[string]any)
			if !ok {
				v = nil
				break
			}
			v = m[part]
		}
		if v == nil {
			return false
		}
		if ok, _ := path.Match(pattern, Text(v)); !ok {
			return false
		}
	}
	return true
}

// --- Webhooks ---

// Fire fires the workflows with a webhook trigger called name, with data
// as their trigger's data. It returns the runs it started.
func (s *Service) Fire(ctx context.Context, name string, data any) ([]RunSummary, error) {
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	if !idPattern.MatchString(name) {
		return nil, invalid("webhook %q: use letters, digits, '.', '_' and '-'", name)
	}
	// Data is what expressions read: JSON's shapes only.
	var payload any
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			return nil, invalid("webhook data: %v", err)
		}
		_ = json.Unmarshal(raw, &payload)
	}
	type hit struct {
		workflow string
		index    int
	}
	var hits []hit
	s.mu.Lock()
	for _, wname := range sortedKeys(s.defs) {
		w := s.defs[wname]
		if !s.firesLocked(w) {
			continue
		}
		for i, tr := range w.Spec.Triggers {
			if tr.Webhook == name {
				hits = append(hits, hit{wname, i})
			}
		}
	}
	s.mu.Unlock()
	out := []RunSummary{}
	for _, h := range hits {
		r, ok := s.fire(ctx, h.workflow, h.index, Trigger{
			Type: TriggerWebhook, Index: h.index, Time: s.cfg.Now(),
			Data: map[string]any{"name": name, "data": payload},
		}, "")
		if ok {
			out = append(out, r.Summary())
		}
	}
	return out, nil
}

// FirePlugin fires the workflows with a trigger of plugin's named name
// (plugin/name) whose match the payload fits. It returns the runs it
// started.
func (s *Service) FirePlugin(ctx context.Context, plugin, name string, payload map[string]any) ([]RunSummary, error) {
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	if !idPattern.MatchString(plugin) || !idPattern.MatchString(name) {
		return nil, invalid("trigger %s/%s: use letters, digits, '.', '_' and '-'", plugin, name)
	}
	// What expressions read: JSON's shapes only.
	var data map[string]any
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, invalid("trigger payload: %v", err)
		}
		_ = json.Unmarshal(raw, &data)
	}
	full := plugin + "/" + name
	type hit struct {
		workflow string
		index    int
	}
	var hits []hit
	s.mu.Lock()
	for _, wname := range sortedKeys(s.defs) {
		w := s.defs[wname]
		if !s.firesLocked(w) {
			continue
		}
		for i, tr := range w.Spec.Triggers {
			if tr.Plugin == full && fieldsMatch(tr.Match, data) {
				hits = append(hits, hit{wname, i})
			}
		}
	}
	s.mu.Unlock()
	out := []RunSummary{}
	for _, h := range hits {
		r, ok := s.fire(ctx, h.workflow, h.index, Trigger{
			Type: TriggerPlugin, Index: h.index, Time: s.cfg.Now(),
			Data: map[string]any{"plugin": plugin, "name": name, "data": data},
		}, "")
		if ok {
			out = append(out, r.Summary())
		}
	}
	return out, nil
}

// --- Firing ---

// fire starts a run of a workflow for its trigger index, unless its
// concurrency says not to. dir is where the trigger says it runs.
func (s *Service) fire(ctx context.Context, name string, index int, tr Trigger, dir string) (Run, bool) {
	s.mu.Lock()
	w := s.defs[name]
	if w == nil || !s.firesLocked(w) || index >= len(w.Spec.Triggers) {
		s.mu.Unlock()
		return Run{}, false
	}
	spec, source, file := *w.Spec, string(w.source), w.Path
	queued, running := s.pendingLocked(name)
	s.mu.Unlock()
	log := s.log.With("workflow", name, "trigger", tr.Type)
	switch {
	case spec.Concurrency == ConcurrencySkip && queued+running > 0:
		log.Info("workflow trigger skipped: a run goes on (concurrency = \"skip\")")
		return Run{}, false
	case spec.Concurrency != ConcurrencyParallel && queued >= MaxQueued:
		log.Warn("workflow trigger dropped: too many runs wait", "queued", queued)
		return Run{}, false
	}
	ctxVars := Context{Vars: map[string]any{"trigger": withType(tr)}}
	inputs := map[string]string{}
	for _, k := range sortedKeys(spec.Triggers[index].Inputs) {
		v, err := render(spec.Triggers[index].Inputs[k], ctxVars)
		if err != nil {
			log.Warn("workflow trigger's inputs", "input", k, "err", err)
			return Run{}, false
		}
		inputs[k] = v
	}
	r, err := s.newRun(spec, source, file, inputs, tr, cmpStr(dir, defaultDir(file)))
	if err != nil {
		log.Warn("workflow trigger refused", "err", err)
		return Run{}, false
	}
	run, err := s.accept(ctx, r)
	if err != nil {
		log.Warn("workflow trigger refused", "err", err)
		return Run{}, false
	}
	return run, true
}

func withType(tr Trigger) map[string]any {
	m := maps.Clone(tr.Data)
	if m == nil {
		m = map[string]any{}
	}
	m["type"] = tr.Type
	return m
}

// --- Git ---

// gitWatch is a git trigger to check against a repository's references.
type gitWatch struct {
	workflow string
	index    int
	spec     TriggerSpec
	repo     string // its root; "" watches every repository
}

// scanGit reads the watched repositories' references, and fires the git
// triggers whose pushes or new branches appeared since they were last
// read (by this daemon or the previous one).
func (t *triggers) scanGit(ctx context.Context) {
	ws := t.s.ports.Workspace
	if ws == nil {
		return
	}
	var watches []gitWatch
	t.s.mu.Lock()
	for _, name := range sortedKeys(t.s.defs) {
		w := t.s.defs[name]
		if !t.s.firesLocked(w) {
			continue
		}
		for i, tr := range w.Spec.Triggers {
			if tr.Type() == TriggerGit {
				watches = append(watches, gitWatch{workflow: name, index: i, spec: tr, repo: t.expand(tr.Repo)})
			}
		}
	}
	t.s.mu.Unlock()
	if len(watches) == 0 {
		return
	}
	repos := map[string]bool{}
	all := false
	for i := range watches {
		if watches[i].repo == "" {
			all = true
			continue
		}
		root, err := ws.RepoRoot(ctx, watches[i].repo)
		if err != nil || root == "" {
			t.s.log.Debug("a workflow's repository is not one", "repo", watches[i].repo, "err", err)
			watches[i].repo = "-" // never matches
			continue
		}
		watches[i].repo = root
		repos[root] = true
	}
	if all {
		list, err := ws.Repos(ctx)
		if err != nil {
			t.s.log.Debug("listing repositories for git triggers", "err", err)
		}
		for _, r := range list {
			repos[r] = true
		}
	}
	type fired struct {
		watch gitWatch
		data  map[string]any
	}
	var fires []fired
	dirty := false
	for _, repo := range sortedKeys(repos) {
		refs, err := ws.Refs(ctx, repo)
		if err != nil {
			t.s.log.Debug("reading a repository's references", "repo", repo, "err", err)
			continue
		}
		t.mu.Lock()
		prev, seen := t.state.Refs[repo]
		if !maps.Equal(prev, refs) {
			t.state.Refs[repo] = refs
			dirty = true
		}
		t.mu.Unlock()
		if !seen {
			continue // the first look: what is there now is the baseline
		}
		for _, ev := range gitEvents(repo, prev, refs) {
			for _, w := range watches {
				if (w.repo == "" || w.repo == repo) && gitMatches(w.spec, ev) {
					fires = append(fires, fired{watch: w, data: ev})
				}
			}
		}
	}
	if dirty {
		t.mu.Lock()
		st := t.cloneStateLocked()
		t.mu.Unlock()
		if err := t.s.store.SaveState(st); err != nil {
			t.s.log.Warn("saving workflow trigger state", "err", err)
		}
	}
	for _, f := range fires {
		t.s.fire(ctx, f.watch.workflow, f.watch.index, Trigger{
			Type: TriggerGit, Index: f.watch.index, Time: t.s.cfg.Now(), Data: f.data,
		}, f.data["repo"].(string))
	}
}

// expand makes "~/" in a trigger's repository absolute.
func (t *triggers) expand(repo string) string {
	if rest, ok := strings.CutPrefix(repo, "~/"); ok && t.s.cfg.Home != "" {
		return filepath.Join(t.s.cfg.Home, rest)
	}
	return repo
}

// gitEvents compares a repository's references with what was seen
// before: a new local branch is a branch event, and a remote-tracking
// branch that moved to its local branch's commit is a push.
func gitEvents(repo string, prev, refs map[string]string) []map[string]any {
	var out []map[string]any
	for _, ref := range sortedKeys(refs) {
		commit := refs[ref]
		if b, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			if _, had := prev[ref]; !had {
				out = append(out, map[string]any{"git": GitBranch, "repo": repo, "branch": b, "commit": commit})
			}
			continue
		}
		rest, ok := strings.CutPrefix(ref, "refs/remotes/")
		if !ok {
			continue
		}
		remote, b, ok := strings.Cut(rest, "/")
		if !ok || b == "HEAD" || prev[ref] == commit {
			continue
		}
		if refs["refs/heads/"+b] != commit {
			continue // fetched someone else's work, not pushed from here
		}
		out = append(out, map[string]any{
			"git": GitPush, "repo": repo, "remote": remote, "branch": b, "commit": commit, "before": prev[ref],
		})
	}
	return out
}

func gitMatches(spec TriggerSpec, ev map[string]any) bool {
	if ev["git"] != spec.Git {
		return false
	}
	if spec.Remote != "" && ev["remote"] != spec.Remote {
		return false
	}
	if len(spec.Branches) == 0 {
		return true
	}
	b, _ := ev["branch"].(string)
	return slices.ContainsFunc(spec.Branches, func(p string) bool {
		ok, _ := path.Match(p, b)
		return ok
	})
}
