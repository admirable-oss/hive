package web

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/insights"
	"github.com/admirable-oss/hive/internal/observer"
)

// Notification kinds.
const (
	KindBlocked = "blocked" // an agent waits for someone's decision
	KindDone    = "done"    // an agent finished a task
	KindFailed  = "failed"  // an agent's program exited with an error
	KindBudget  = "budget"  // a budget warns or is exceeded (insights)
	KindTest    = "test"
)

// Notification rates.
const (
	// perMinute bounds one device's notifications; more are summed up in
	// one "and N more" after the minute.
	perMinute   = 6
	queueLength = 256
	senders     = 2
	sendTimeout = 15 * time.Second
)

// Notification is the payload a device receives: never screen contents.
type Notification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Tag   string `json:"tag"`
	Kind  string `json:"kind"`
	Time  int64  `json:"ts"`
}

// mark is what was last notified about an agent: notifications fire on
// changes past it, so a reconnect, a resync or a repeat never fires twice.
type mark struct {
	state      agent.State
	stateSeq   uint64
	completion uint64
	exited     bool
}

type pushJob struct {
	sub Subscription
	msg pushMessage
}

// notifier turns agent transitions into Web Push notifications for the
// devices subscribed to them. It keeps connected every runtime a device
// may be notified about, so notifications arrive with no page open.
type notifier struct {
	s    *Server
	push *pusher
	log  *slog.Logger

	mu      sync.Mutex
	held    map[string]func()          // runtime → release and stop listening
	marks   map[string]map[string]mark // runtime → agent → mark (nil: no snapshot yet)
	buckets map[string]*deviceRate     // subscription → rate
	queue   chan pushJob
	stop    chan struct{}
	wg      sync.WaitGroup
	// transition is called for each transition found (notify; tests
	// record them instead).
	transition func(rt, kind string, a observer.Agent, detail string)
	// alert is called for each budget alert (notifyAlert; tests record
	// them instead).
	alert func(rt string, a insights.Alert)
}

type deviceRate struct {
	sent    []time.Time
	pending int
}

func newNotifier(s *Server, p *pusher) *notifier {
	n := &notifier{
		s: s, push: p, log: s.log.With("component", "push"),
		held: map[string]func(){}, marks: map[string]map[string]mark{}, buckets: map[string]*deviceRate{},
		queue: make(chan pushJob, queueLength), stop: make(chan struct{}),
	}
	n.transition = n.notify
	n.alert = n.notifyAlert
	for range senders {
		n.wg.Go(n.sender)
	}
	n.wg.Go(n.summaries)
	return n
}

// close stops the notifier and lets go of its runtimes.
func (n *notifier) close() {
	n.mu.Lock()
	held := n.held
	n.held = map[string]func(){}
	n.mu.Unlock()
	for _, release := range held {
		release()
	}
	close(n.stop)
	n.wg.Wait()
}

// refresh holds exactly the runtimes some device may hear from.
func (n *notifier) refresh() {
	subs, err := n.s.store.Subscriptions("")
	if err != nil {
		return
	}
	need := map[string]bool{}
	for _, sub := range subs {
		if _, g, ok := n.s.store.Active(sub.Session); ok {
			for _, rt := range n.s.allowed(g) {
				need[rt.Name] = true
			}
		}
	}
	n.mu.Lock()
	var releases []func()
	for rt, release := range n.held {
		if !need[rt] {
			releases = append(releases, release)
			delete(n.held, rt)
			delete(n.marks, rt)
		}
	}
	var start []string
	for rt := range need {
		if _, ok := n.held[rt]; !ok {
			start = append(start, rt)
		}
	}
	n.mu.Unlock()
	for _, release := range releases {
		release()
	}
	for _, rt := range start {
		if c := n.s.conn(rt); c != nil {
			n.hold(rt, c)
		}
	}
}

// hold keeps runtime rt connected and listens to it.
func (n *notifier) hold(rt string, c *conn) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.held[rt]; ok { // another refresh got there first
		return
	}
	{
		// Listen and take the marks from what is known now, together: an
		// event between them waits for n.mu and finds the marks set.
		stopListening := c.Listen(func(u observer.Update) { n.update(rt, u) })
		if v := c.model.View(); v.Loaded() {
			marks := map[string]mark{}
			for id, a := range v.Agents {
				marks[id] = mark{state: a.State, stateSeq: a.StateSeq, completion: a.CompletionSeq}
			}
			n.marks[rt] = marks
		}
		release := c.Acquire()
		n.held[rt] = func() { stopListening(); release() }
	}
}

// PushStatus is what `hive web push status` shows.
type PushStatus struct {
	Enabled   bool            `json:"enabled"`
	PublicKey string          `json:"public_key,omitempty"`
	Devices   []Subscription  `json:"devices"`
	Runtimes  map[string]bool `json:"runtimes"` // held for notifications → ready (seen a snapshot)
}

func (n *notifier) status() PushStatus {
	devices, _ := n.s.store.Subscriptions("")
	for i := range devices {
		devices[i].P256dh, devices[i].Auth = "", ""
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	st := PushStatus{Enabled: true, PublicKey: n.push.key.PublicKey(), Devices: devices, Runtimes: map[string]bool{}}
	for rt := range n.held {
		_, primed := n.marks[rt]
		st.Runtimes[rt] = primed
	}
	return st
}

// update looks for transitions worth a notification in what a runtime
// sent.
func (n *notifier) update(rt string, u observer.Update) {
	n.mu.Lock()
	marks, primed := n.marks[rt]
	if !primed {
		marks = map[string]mark{}
		n.marks[rt] = marks
	}
	var fire []func()
	note := func(kind string, a observer.Agent, detail string) {
		fire = append(fire, func() { n.transition(rt, kind, a, detail) })
	}
	switch {
	case u.Snapshot != nil:
		seen := map[string]bool{}
		for _, a := range u.Snapshot.Agents {
			seen[a.ID] = true
			old, known := marks[a.ID]
			marks[a.ID] = mark{state: a.State, stateSeq: a.StateSeq, completion: a.CompletionSeq, exited: old.exited}
			if !primed || !known {
				continue // the first look at a runtime only sets the marks
			}
			// Missed while disconnected (a resync): still worth telling.
			if a.State == agent.StateBlocked && a.StateSeq > old.stateSeq {
				note(KindBlocked, a, a.Reason)
			}
			if a.CompletionSeq > old.completion {
				note(KindDone, a, "")
			}
		}
		for id := range marks {
			if !seen[id] {
				delete(marks, id)
			}
		}
	case u.Event != nil && u.Event.Type == "agent.state" && primed:
		var a observer.Agent
		if json.Unmarshal(u.Event.Data, &a) != nil {
			break
		}
		old, known := marks[a.ID]
		if known && a.StateSeq < old.stateSeq {
			break // older than what was seen
		}
		marks[a.ID] = mark{state: a.State, stateSeq: a.StateSeq, completion: max(a.CompletionSeq, old.completion), exited: old.exited}
		if a.State == agent.StateBlocked && (!known || a.StateSeq > old.stateSeq) && old.state != agent.StateBlocked {
			note(KindBlocked, a, a.Reason)
		}
		if a.CompletionSeq > old.completion {
			note(KindDone, a, "")
		}
	case u.Event != nil && u.Event.Type == insights.EventAlert && primed:
		// Each level is raised once per budget, key and window, by the
		// daemon: an alert is news whenever it comes.
		var al insights.Alert
		if json.Unmarshal(u.Event.Data, &al) == nil && al.Budget != "" {
			fire = append(fire, func() { n.alert(rt, al) })
		}
	case u.Event != nil && u.Event.Type == "process.exited" && primed:
		var p observer.Process
		if json.Unmarshal(u.Event.Data, &p) != nil || p.ExitCode == nil || *p.ExitCode == 0 {
			break
		}
		m, isAgent := marks[p.ID]
		if !isAgent || m.exited {
			break
		}
		m.exited = true
		marks[p.ID] = m
		a := observer.Agent{ID: p.ID, EnvironmentID: p.EnvironmentID, PaneID: p.PaneID, Name: p.Program}
		if v, ok := n.s.conn(rt).model.View().Agents[p.ID]; ok {
			a = v
		}
		note(KindFailed, a, fmt.Sprintf("exit code %d", *p.ExitCode))
	}
	n.mu.Unlock()
	for _, f := range fire {
		f()
	}
}

// notify tells every device that may hear about agent a in rt.
func (n *notifier) notify(rt, kind string, a observer.Agent, detail string) {
	subs, err := n.s.store.Subscriptions("")
	if err != nil {
		return
	}
	now := n.s.now()
	for _, sub := range subs {
		sess, g, ok := n.s.store.Active(sub.Session)
		if !ok || !slices.Contains(g.Runtimes, rt) || !g.Scope.Sees(observer.Target{Environment: a.EnvironmentID, Pane: a.PaneID}) {
			continue // revoked, expired, or not this device's business
		}
		prefs := n.s.store.PrefsOf(sess.ID)
		if !prefs.wants(kind) || prefs.muted(rt, a.EnvironmentID, a.ID, now) {
			continue
		}
		if !n.allow(sub.ID, now) {
			continue
		}
		n.enqueue(sub, message(rt, kind, a, detail, prefs.Detail, now))
	}
}

// notifyAlert tells every device that may hear about a budget alert in rt.
func (n *notifier) notifyAlert(rt string, al insights.Alert) {
	subs, err := n.s.store.Subscriptions("")
	if err != nil {
		return
	}
	now := n.s.now()
	env := al.Target()
	for _, sub := range subs {
		sess, g, ok := n.s.store.Active(sub.Session)
		if !ok || !slices.Contains(g.Runtimes, rt) || !g.Scope.Sees(observer.Target{Environment: env}) {
			continue
		}
		prefs := n.s.store.PrefsOf(sess.ID)
		if !prefs.wants(KindBudget) || prefs.muted(rt, env, "", now) || !n.allow(sub.ID, now) {
			continue
		}
		n.enqueue(sub, alertMessage(rt, al, prefs.Detail, now))
	}
}

// alertMessage builds a budget alert's notification: amounts, never
// screen contents.
func alertMessage(rt string, al insights.Alert, privacy string, now time.Time) pushMessage {
	sum := sha256.Sum256([]byte(rt + "/budget/" + al.Budget + "/" + al.Key))
	topic := base64.RawURLEncoding.EncodeToString(sum[:])[:32]
	nt := Notification{Kind: KindBudget, Time: now.UnixMilli(), Tag: "hive-" + topic, URL: "/r/" + url.PathEscape(rt) + "?from=notification"}
	switch privacy {
	case DetailPrivate:
		nt.Title, nt.Body = "Hive", "A budget needs your attention"
	default:
		nt.Title = "Budget " + al.Budget + " " + map[string]string{insights.LevelExceeded: "exceeded", insights.LevelWarning: "near its limit"}[al.Level]
		nt.Body = truncate(al.Message, 120) + " · " + rt
	}
	payload, _ := json.Marshal(nt)
	return pushMessage{Payload: payload, TTL: 4 * time.Hour, Urgent: al.Level == insights.LevelExceeded, Topic: topic}
}

func (p Prefs) wants(kind string) bool {
	switch kind {
	case KindBlocked:
		return p.Blocked
	case KindDone:
		return p.Done
	case KindFailed:
		return p.Failed
	}
	return true
}

// message builds a notification. With private detail it names nothing;
// either way it carries no screen contents.
func message(rt, kind string, a observer.Agent, detail, privacy string, now time.Time) pushMessage {
	what := map[string]string{KindBlocked: "is waiting for you", KindDone: "finished", KindFailed: "stopped with an error"}[kind]
	// The tag (which replaces an agent's earlier notification on the
	// device) and the topic are a hash: they name nothing either.
	sum := sha256.Sum256([]byte(rt + "/" + a.ID))
	topic := base64.RawURLEncoding.EncodeToString(sum[:])[:32]
	nt := Notification{Kind: kind, Time: now.UnixMilli(), Tag: "hive-" + topic, URL: "/attention?from=notification"}
	switch privacy {
	case DetailPrivate:
		nt.Title = "Hive"
		nt.Body = map[string]string{KindBlocked: "An agent is waiting for you", KindDone: "An agent finished", KindFailed: "An agent stopped with an error"}[kind]
	default:
		nt.Title = a.Name + " " + what
		nt.Body = a.Kind + " in " + a.EnvironmentID + " on " + rt
		if detail != "" && kind != KindBlocked {
			nt.Body += " · " + detail
		}
		if kind == KindBlocked && detail != "" {
			nt.Body += " · " + truncate(detail, 80)
		}
		if a.PaneID != "" {
			nt.URL = "/r/" + url.PathEscape(rt) + "/p/" + url.PathEscape(a.PaneID) + "?from=notification"
		}
	}
	payload, _ := json.Marshal(nt)
	ttl := time.Hour
	if kind == KindBlocked {
		ttl = 4 * time.Hour
	}
	return pushMessage{Payload: payload, TTL: ttl, Urgent: kind == KindBlocked, Topic: topic}
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// allow takes one notification from a device's minute; past it, the
// notification is counted for the summary.
func (n *notifier) allow(id string, now time.Time) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	b := n.buckets[id]
	if b == nil {
		b = &deviceRate{}
		n.buckets[id] = b
	}
	b.sent = slices.DeleteFunc(b.sent, func(t time.Time) bool { return now.Sub(t) >= time.Minute })
	if len(b.sent) >= perMinute {
		b.pending++
		return false
	}
	b.sent = append(b.sent, now)
	return true
}

// summaries sends "and N more" to devices that hit their limit, once the
// minute has passed.
func (n *notifier) summaries() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-n.stop:
			return
		case <-t.C:
		}
		n.mu.Lock()
		due := map[string]int{}
		for id, b := range n.buckets {
			if b.pending > 0 {
				due[id], b.pending = b.pending, 0
			}
		}
		n.mu.Unlock()
		if len(due) == 0 {
			continue
		}
		subs, _ := n.s.store.Subscriptions("")
		for _, sub := range subs {
			if count := due[sub.ID]; count > 0 {
				if _, _, ok := n.s.store.Active(sub.Session); ok {
					payload, _ := json.Marshal(Notification{Title: "Hive", Body: fmt.Sprintf("%d more updates about your agents", count), URL: "/attention?from=notification", Tag: "hive-summary", Kind: "summary", Time: n.s.now().UnixMilli()})
					n.enqueue(sub, pushMessage{Payload: payload, TTL: time.Hour, Topic: "hive-summary"})
				}
			}
		}
	}
}

func (n *notifier) enqueue(sub Subscription, m pushMessage) {
	select {
	case n.queue <- pushJob{sub: sub, msg: m}:
	default:
		n.log.Warn("push queue full; a notification was dropped", "device", sub.String())
	}
}

func (n *notifier) sender() {
	for {
		select {
		case <-n.stop:
			return
		case job := <-n.queue:
			ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
			err := n.push.send(ctx, job.sub, job.msg)
			cancel()
			n.s.store.Delivered(job.sub.ID, err)
			var kind struct {
				Kind string `json:"kind"`
			}
			_ = json.Unmarshal(job.msg.Payload, &kind)
			switch {
			case err == nil:
				n.s.audit.Record(observer.AuditEntry{Action: "push_sent", Detail: kind.Kind + " to device " + job.sub.ID})
			case errors.Is(err, errGone):
				n.s.audit.Record(observer.AuditEntry{Action: "push_dropped", Detail: "device " + job.sub.ID + " is gone"})
			default:
				n.log.Warn("push failed", "device", job.sub.String(), "err", err)
			}
		}
	}
}

// sendTest notifies every device of session sid.
func (n *notifier) sendTest(sid string) (int, error) {
	subs, err := n.s.store.Subscriptions(sid)
	if err != nil {
		return 0, err
	}
	for _, sub := range subs {
		payload, _ := json.Marshal(Notification{Title: "Hive", Body: "Notifications work on this device.", URL: "/settings", Tag: "hive-test", Kind: KindTest, Time: n.s.now().UnixMilli()})
		n.enqueue(sub, pushMessage{Payload: payload, TTL: 10 * time.Minute, Topic: "hive-test"})
	}
	return len(subs), nil
}
