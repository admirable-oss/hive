package runtime

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/edits"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/git"
)

// newEdits follows the files agents change, for editors (M31).
func newEdits(envs environment.Service, agents *agent.Service, bus *event.Bus, log *slog.Logger) *edits.Service {
	return edits.NewService(edits.Config{Logger: log}, agents, envs, git.New(), bus)
}

// feedEdits tells the service when agents change state, so a working
// tree is read the moment an agent starts working in it.
func feedEdits(ctx context.Context, svc *edits.Service, bus *event.Bus) {
	sub := bus.Subscribe(event.DefaultBuffer, agent.EventState)
	defer sub.Close()
	for {
		ev, err := sub.Next(ctx)
		if err != nil {
			return
		}
		var a agent.Agent
		if ev.Type == agent.EventState && json.Unmarshal(ev.Data, &a) == nil {
			svc.Changed(a)
		}
	}
}
