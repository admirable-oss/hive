package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/branch"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/issue"
	"github.com/admirable-oss/hive/internal/plugin"
	"github.com/admirable-oss/hive/internal/task"
)

// Issues come from GitHub through gh, and from the trackers plugins
// provide; this file fills in the issue service's ports and closes a
// task's issue when the task's branch merges, if the task says so.

// newIssues builds the issue service over gh and the plugins' trackers.
func newIssues(plugins *plugin.Host, bus event.Publisher) *issue.Service {
	return issue.NewService(issue.GitHubTracker{Exec: issue.Exec(branch.ExecCommand)}, pluginTrackers{host: plugins}, bus)
}

// pluginTrackers are the trackers the loaded plugins provide.
type pluginTrackers struct{ host *plugin.Host }

func (p pluginTrackers) Tracker(name string) (issue.Tracker, bool) {
	if p.host == nil {
		return nil, false
	}
	t, ok := p.host.TrackerFor(name)
	if !ok {
		return nil, false
	}
	return pluginTracker{host: p.host, t: t}, true
}

func (p pluginTrackers) Names() []string {
	if p.host == nil {
		return nil
	}
	return p.host.TrackerNames()
}

// pluginTracker calls one plugin tracker's command.
type pluginTracker struct {
	host *plugin.Host
	t    plugin.PluginTracker
}

func (p pluginTracker) call(ctx context.Context, req plugin.TrackerRequest, into any) error {
	raw, err := p.host.CallTracker(ctx, p.t, req)
	if err != nil || into == nil {
		return err
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("%w: tracker %s answered %s: %v", plugin.ErrCall, p.t.Decl.Name, req.Op, err)
	}
	return nil
}

func (p pluginTracker) issue(i issue.Issue, key string) (issue.Issue, error) {
	if i.Key == "" {
		i.Key = key
	}
	if i.Key == "" || i.Title == "" {
		return issue.Issue{}, fmt.Errorf("%w: tracker %s answered an issue without its key or title", plugin.ErrCall, p.t.Decl.Name)
	}
	if i.URL != "" && !strings.HasPrefix(i.URL, "https://") {
		i.URL = "" // only https links are shown, as everywhere
	}
	return i, nil
}

func (p pluginTracker) Get(ctx context.Context, key string) (issue.Issue, error) {
	var i issue.Issue
	if err := p.call(ctx, plugin.TrackerRequest{Op: plugin.TrackerOpGet, Key: key}, &i); err != nil {
		return issue.Issue{}, err
	}
	return p.issue(i, key)
}

func (p pluginTracker) Search(ctx context.Context, query string, limit int) ([]issue.Issue, error) {
	var res struct {
		Issues []issue.Issue `json:"issues"`
	}
	if err := p.call(ctx, plugin.TrackerRequest{Op: plugin.TrackerOpSearch, Query: query, Limit: limit}, &res); err != nil {
		return nil, err
	}
	out := make([]issue.Issue, 0, len(res.Issues))
	for _, i := range res.Issues {
		if i, err := p.issue(i, ""); err == nil {
			out = append(out, i)
		}
	}
	return out, nil
}

func (p pluginTracker) Comment(ctx context.Context, key, body string) error {
	return p.call(ctx, plugin.TrackerRequest{Op: plugin.TrackerOpComment, Key: key, Body: body}, nil)
}

func (p pluginTracker) Close(ctx context.Context, key, comment string) (issue.Issue, error) {
	var i issue.Issue
	if err := p.call(ctx, plugin.TrackerRequest{Op: plugin.TrackerOpClose, Key: key, Comment: comment}, &i); err != nil {
		return issue.Issue{}, err
	}
	return p.issue(i, key)
}

// issueLinks names a task's issue in its branch's pull request (the
// branch service is built before the tasks: tasks is set later).
type issueLinks struct {
	tasks  planReader
	issues *issue.Service
}

// planReader reads a plan (task.Service).
type planReader interface {
	Get(ctx context.Context, id string) (task.Plan, error)
}

func (l *issueLinks) For(ctx context.Context, planID, taskID string) string {
	t, ok := l.taskOf(ctx, planID, taskID)
	if !ok || t.Spec.Issue == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if i, err := l.issues.Get(ctx, t.Spec.Issue); err == nil && i.URL != "" {
		return i.URL
	}
	return t.Spec.Issue
}

func (l *issueLinks) taskOf(ctx context.Context, planID, taskID string) (task.Task, bool) {
	if l.tasks == nil {
		return task.Task{}, false
	}
	p, err := l.tasks.Get(ctx, planID)
	if err != nil {
		return task.Task{}, false
	}
	for _, t := range p.Tasks {
		if t.Spec.ID == taskID {
			return t, true
		}
	}
	return task.Task{}, false
}

// closeMergedIssues closes the issue of a task whose branch merged, when
// the task says close_issue, until the daemon stops. An issue already
// closed is left alone, so a restart closes nothing twice.
func closeMergedIssues(ctx context.Context, bus *event.Bus, links *issueLinks, log *slog.Logger) {
	sub := bus.Subscribe(event.DefaultBuffer, branch.EventUpdated)
	defer sub.Close()
	done := map[string]bool{}
	for {
		ev, err := sub.Next(ctx)
		if err != nil {
			return
		}
		var b branch.Branch
		if json.Unmarshal(ev.Data, &b) != nil || b.State != branch.StateMerged || b.Owner == nil || b.Owner.Task == "" {
			continue
		}
		key := b.Owner.Plan + "/" + b.Owner.Task
		if done[key] {
			continue
		}
		t, ok := links.taskOf(ctx, b.Owner.Plan, b.Owner.Task)
		if !ok || !t.Spec.CloseIssue || t.Spec.Issue == "" {
			continue
		}
		done[key] = true
		closeIssue(ctx, links.issues, t.Spec.Issue, b, log)
	}
}

func closeIssue(ctx context.Context, issues *issue.Service, ref string, b branch.Branch, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	log = log.With("issue", ref, "branch", b.Name)
	if i, err := issues.Get(ctx, ref); err != nil {
		log.Warn("read the merged task's issue", "err", err)
		return
	} else if i.Closed() {
		return
	}
	comment := "Merged: " + b.Name
	if b.PullRequest != nil {
		comment += " (" + b.PullRequest.URL + ")"
	}
	if _, err := issues.Close(ctx, ref, comment+", by a task in Hive."); err != nil {
		log.Warn("close the merged task's issue", "err", err)
		return
	}
	log.Info("closed the merged task's issue")
}
