// Command observer-dashboard shows what every agent is doing, live, from an
// observer token: it can watch Hive but never drive it.
//
//	hive observer create dashboard --all          # prints a token
//	HIVE_OBSERVER_TOKEN=hvo_… go run ./examples/observer-dashboard
//	HIVE_OBSERVER_TOKEN=hvo_… go run ./examples/observer-dashboard -host buildbox
//
// It keeps a model of the agents current with hiveapi.Follow: a snapshot,
// then events applied to it, and a new snapshot whenever events were missed
// (it reconnects by itself when the daemon restarts or the network drops).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// board is the dashboard's model: agents by ID.
type board struct {
	agents map[string]hiveapi.ObservedAgent
	seq    uint64
}

func (b *board) apply(u hiveapi.ObserverUpdate) (changed bool) {
	if u.Snapshot != nil {
		b.agents = map[string]hiveapi.ObservedAgent{}
		for _, a := range u.Snapshot.Agents {
			b.agents[a.ID] = a
		}
		b.seq = u.Snapshot.Seq
		return true
	}
	ev := u.Event
	b.seq = ev.Seq
	switch ev.Type {
	case "agent.state":
		var a hiveapi.ObservedAgent
		if json.Unmarshal(ev.Data, &a) != nil {
			return false
		}
		// Applied by ID, so an event the snapshot already showed is harmless.
		b.agents[a.ID] = a
		return true
	case "pane.closed":
		var p hiveapi.ObservedPane
		if json.Unmarshal(ev.Data, &p) != nil {
			return false
		}
		for id, a := range b.agents {
			if a.PaneID == p.ID {
				delete(b.agents, id)
				changed = true
			}
		}
	}
	return changed
}

func (b *board) print() {
	fmt.Print("\033[H\033[2J") // clear the terminal
	fmt.Printf("Hive agents — %s (event %d)\n\n", time.Now().Format(time.TimeOnly), b.seq)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "AGENT\tKIND\tENVIRONMENT\tSTATE\tFOR\tWHY")
	ids := make([]string, 0, len(b.agents))
	for id := range b.agents {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(x, y string) int { return strings.Compare(b.agents[x].Name, b.agents[y].Name) })
	for _, id := range ids {
		a := b.agents[id]
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", a.Name, a.Kind, a.EnvironmentID, a.State, time.Since(a.Since).Round(time.Second), a.Reason)
	}
	_ = w.Flush()
}

func main() {
	host := flag.String("host", "", "watch Hive on this ssh host instead of this machine")
	flag.Parse()
	if err := run(*host); err != nil {
		log.Fatalf("observer-dashboard: %v", err) // revoked, expired, or no token
	}
}

func run(host string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	b := &board{}
	return hiveapi.Follow(ctx, hiveapi.ObserverOptions{Host: host, Name: "observer-dashboard"}, func(u hiveapi.ObserverUpdate) error {
		if b.apply(u) {
			b.print()
		}
		return nil
	})
}
