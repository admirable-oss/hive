package runtime

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/terminal"
)

// The agent service watches terminals, so it is built before the terminal
// module, and before the processes and panes it reads. These hold those
// services once they exist; the agent service only reads them after the
// daemon starts.
type (
	lateProcesses struct{ process.Service }
	lateTerminals struct{ terminal.Service }
	paneLocator   struct{ panes *pane.Service }
)

// Locate tells the agent service which pane shows each process.
func (l *paneLocator) Locate(ctx context.Context) (map[string]agent.Location, error) {
	panes, err := l.panes.Panes(ctx, "", "")
	if err != nil {
		return nil, err
	}
	out := make(map[string]agent.Location, len(panes))
	for _, p := range panes {
		out[p.ProcessID] = agent.Location{PaneID: p.ID, TabID: p.TabID, Name: p.Name}
	}
	return out, nil
}

// markSeen tells the agent service when a pane is focused: a done agent
// someone looked at is idle again.
func markSeen(ctx context.Context, bus *event.Bus, agents *agent.Service, log *slog.Logger) {
	sub := bus.Subscribe(event.DefaultBuffer, pane.EventPaneFocused)
	defer sub.Close()
	for {
		ev, err := sub.Next(ctx)
		if err != nil {
			return
		}
		if ev.Type == event.Lost {
			continue
		}
		var p pane.Pane
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			log.Debug("undecodable pane event", "err", err)
			continue
		}
		if p.ProcessID != "" {
			_ = agents.Seen(ctx, p.ProcessID)
		}
	}
}
