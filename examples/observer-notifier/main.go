// Command observer-notifier tells a chat channel (any webhook taking JSON)
// when an agent needs someone: it is blocked on a question, or done. It
// holds only an observer token, so it can read agents' states and screens
// but cannot answer them.
//
//	hive observer create notifier --env api --screens --expires 720h
//	HIVE_OBSERVER_TOKEN=hvo_… go run ./examples/observer-notifier -webhook https://hooks.example.com/T0/B0/x
//
// Without -webhook it prints the notifications. Each agent's state change
// is reported once, by its state_seq, even across reconnects and resyncs.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

type notifier struct {
	webhook string
	lines   int
	obs     *hiveapi.Observer // for screens; nil without --screens access
	told    map[string]uint64 // agent ID → last state_seq reported
}

func (n *notifier) consider(ctx context.Context, a hiveapi.ObservedAgent) {
	if a.State != hiveapi.StateBlocked && a.State != hiveapi.StateDone {
		return
	}
	if n.told[a.ID] >= a.StateSeq {
		return // already reported (or seen in a snapshot after a resync)
	}
	n.told[a.ID] = a.StateSeq
	text := fmt.Sprintf("%s (%s, in %s) is %s", a.Name, a.Kind, a.EnvironmentID, a.State)
	if a.Reason != "" {
		text += ": " + a.Reason
	}
	if n.obs != nil && a.PaneID != "" {
		if out, err := n.obs.Output(ctx, hiveapi.OutputRequest{Pane: a.PaneID, Lines: n.lines}); err == nil {
			text += "\n```\n" + strings.Join(out.Lines, "\n") + "\n```"
		}
	}
	n.send(ctx, text)
}

func (n *notifier) send(ctx context.Context, text string) {
	if n.webhook == "" {
		fmt.Println(text)
		return
	}
	body, _ := json.Marshal(map[string]string{"text": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.webhook, bytes.NewReader(body))
	if err != nil {
		log.Print(err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("webhook: %v", err)
		return
	}
	_ = resp.Body.Close()
}

func main() {
	webhook := flag.String("webhook", "", "URL to POST {\"text\": …} to (default: print)")
	lines := flag.Int("lines", 15, "lines of the agent's screen to include (needs a token made with --screens)")
	flag.Parse()
	if err := run(&notifier{webhook: *webhook, lines: *lines, told: map[string]uint64{}}); err != nil {
		log.Fatalf("observer-notifier: %v", err)
	}
}

func run(n *notifier) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	opts := hiveapi.ObserverOptions{Name: "observer-notifier"}
	// A second connection reads screens; Follow owns the first.
	if o, err := hiveapi.Observe(ctx, opts); err == nil && o.Credential().Screens {
		n.obs = o
		defer o.Close()
	} else if err == nil {
		_ = o.Close()
	}
	first := true
	return hiveapi.Follow(ctx, opts, func(u hiveapi.ObserverUpdate) error {
		if u.Snapshot != nil {
			for _, a := range u.Snapshot.Agents {
				if first {
					// Do not announce what was already so at start.
					n.told[a.ID] = a.StateSeq
				} else {
					n.consider(ctx, a)
				}
			}
			first = false
			return nil
		}
		if u.Event.Type == "agent.state" {
			var a hiveapi.ObservedAgent
			if err := json.Unmarshal(u.Event.Data, &a); err == nil {
				n.consider(ctx, a)
			}
		}
		return nil
	})
}
