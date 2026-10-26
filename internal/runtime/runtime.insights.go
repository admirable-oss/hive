package runtime

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/insights"
	"github.com/admirable-oss/hive/internal/task"
)

// Insights follow agents' and tasks' events through the ports package
// insights declares; this file fills them in, and has budgets hold tasks
// through the task service's Hold.

// InsightsConfig is the [insights] policy, with [retention]'s bound on the
// days kept. Zero values are the defaults.
type InsightsConfig struct {
	// Disabled records nothing; what was recorded can still be read.
	Disabled bool
	// KeepDays is how many days of records are kept
	// (insights.DefaultKeepDays).
	KeepDays int
	// Budgets are the [[insights.budget]] tables, already checked.
	Budgets []insights.Budget
}

// newInsights builds the insights service over this daemon's agents and
// tasks. Its events wake the task scheduler when a budget's hold may have
// changed.
func newInsights(cfg Config, agents *agent.Service, tasks *insightsTasks, bus event.Publisher, log *slog.Logger) *insights.Service {
	ic := cfg.Insights
	return insights.NewModule(insights.ModuleConfig{
		BaseDir: cfg.root(),
		Service: insights.Config{Logger: log, Disabled: ic.Disabled, KeepDays: ic.KeepDays, Budgets: ic.Budgets},
	}, insights.Ports{
		Agents: agents,
		Tasks:  tasks,
		Events: insightsEvents{bus: bus, tasks: tasks},
	}).Service
}

// feedInsights hands agents' and tasks' events to insights until the
// daemon stops. After missed events every agent and task is handed over
// again: a missed change would keep counting the state before it.
func feedInsights(ctx context.Context, svc *insights.Service, agents *agent.Service, tasks *insightsTasks, bus *event.Bus) {
	sub := bus.Subscribe(event.DefaultBuffer, "agent.", "task.")
	defer sub.Close()
	for {
		ev, err := sub.Next(ctx)
		if err != nil {
			return
		}
		if ev.Type == event.Lost {
			resyncInsights(ctx, svc, agents, tasks)
			continue
		}
		svc.Notify(ctx, ev.Type, ev.Data)
	}
}

// resyncInsights hands insights every agent's state and every task, as
// their events would have.
func resyncInsights(ctx context.Context, svc *insights.Service, agents *agent.Service, tasks *insightsTasks) {
	notify := func(typ string, v any) {
		if raw, err := json.Marshal(v); err == nil {
			svc.Notify(ctx, typ, raw)
		}
	}
	if list, err := agents.List(ctx, false); err == nil {
		for _, a := range list {
			notify(agent.EventState, a)
		}
	}
	if tasks.svc == nil {
		return
	}
	for _, p := range tasks.svc.All(ctx) {
		for i := range p.Tasks {
			notify(task.EventTask, p.Tasks[i].View(&p))
		}
	}
}

// insightsTasks is the task service as insights reads it. The task
// service is built after insights, whose Hold it takes, so svc is set
// once it is; both are before the daemon starts.
type insightsTasks struct {
	svc *task.Service
}

func (t *insightsTasks) TaskOf(ctx context.Context, agentID string) (plan, tsk string) {
	if t.svc == nil {
		return "", ""
	}
	return t.svc.TaskOf(ctx, agentID)
}

// wake has the task scheduler read the holds again.
func (t *insightsTasks) wake() {
	if t.svc != nil {
		t.svc.Wake()
	}
}

// insightsEvents publishes insights' events; an alert (a budget exceeded
// may now hold tasks) or a resume (a hold lifted) wakes the task
// scheduler, so held tasks are marked and released at once.
type insightsEvents struct {
	bus   event.Publisher
	tasks *insightsTasks
}

func (e insightsEvents) Publish(typ string, data any) {
	e.bus.Publish(typ, data)
	if typ == insights.EventAlert || typ == insights.EventResumed {
		e.tasks.wake()
	}
}
