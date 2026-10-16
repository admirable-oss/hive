package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/runtime"
)

// observerTokenEnv holds an observer token for the commands that act as
// an observer. Tokens are never taken as flags, which other users can read
// in the process list.
const observerTokenEnv = "HIVE_OBSERVER_TOKEN"

func newObserverCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "observer",
		Short: "Give programs read-only access to Hive: dashboards, notifiers",
		Long: `Observers watch Hive without driving it. An observer credential names what
it may see (every environment, or some environments and panes), whether it may
read screen contents, and when it expires. Programs holding its token connect
to the daemon's observer socket, where every method only reads: they can follow
agents' states, events and screens, but never type, change layouts or start
anything.

Managing credentials (create, list, show, revoke, validate, audit) needs
control of the daemon. The other commands act as an observer, with the token
in $` + observerTokenEnv + ` or --token-file.

Observer access is enforced by the daemon's API, not by the operating system:
a program running as your user can still open the control socket. See
docs/observers.md.`,
	}
	cmd.AddCommand(
		newObserverCreateCmd(a),
		newObserverListCmd(a),
		newObserverShowCmd(a),
		newObserverRevokeCmd(a),
		newObserverValidateCmd(a),
		newObserverAuditCmd(a),
		newObserverStatusCmd(a),
		newObserverSnapshotCmd(a),
		newObserverScreenCmd(a),
		newObserverEventsCmd(a),
		newObserverWatchCmd(a),
	)
	return cmd
}

func newObserverCreateCmd(a *app) *cobra.Command {
	var (
		envs, panes []string
		all         bool
		screens     bool
		expires     time.Duration
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "create <name>",
		Short: "Make an observer credential and print its token (shown once)",
		Example: `  hive observer create dashboard --all
  hive observer create ci-watch --env dev --screens --expires 24h
  hive observer create pair --pane p3 --screens`,
		Args: exactArgs(1),
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			scope := observer.Scope{All: all, Environments: envs, Panes: panes}
			if err := scope.Validate(); err != nil {
				return &usageError{errors.New("give --all, or --env and --pane for what it may see (not both)")}
			}
			var res runtime.ObserverCreated
			err := a.client.Call(ctx, "observer.create", runtime.ObserverCreateParams{
				Name: args[0], Scope: scope, Screens: screens, TTLSeconds: int64(expires / time.Second),
			}, &res)
			if err != nil {
				return err
			}
			return a.emit(res, func() error {
				c := res.Credential
				fmt.Fprintf(a.out, "Created observer %s (%s)\n", c.ID, c.Name)
				fmt.Fprintf(a.out, "  scope:    %s\n  screens:  %s\n  expires:  %s\n  socket:   %s\n\n", c.Scope, yesNo(c.Screens), c.ExpiresAt.Local().Format(time.DateTime), res.Socket)
				fmt.Fprintf(a.out, "Token (shown only now; give it to the observer, e.g. as $%s):\n%s\n", observerTokenEnv, res.Token)
				return nil
			})
		}),
	})
	cmd.Flags().StringArrayVar(&envs, "env", nil, "an environment it may see (repeatable)")
	cmd.Flags().StringArrayVar(&panes, "pane", nil, "a pane it may see (repeatable)")
	cmd.Flags().BoolVar(&all, "all", false, "it may see every environment of this session")
	cmd.Flags().BoolVar(&screens, "screens", false, "it may read screen contents and terminal output (default: metadata and events only)")
	cmd.Flags().DurationVar(&expires, "expires", observer.DefaultTTL, "how long it lasts (1m to 8760h)")
	return cmd
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func newObserverListCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List observer credentials, their state and open connections",
		Args:    noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			var res runtime.ObserverList
			if err := a.client.Call(ctx, "observer.list", nil, &res); err != nil {
				return err
			}
			return a.emit(res, func() error {
				if !res.Enabled {
					fmt.Fprintln(a.errOut, "hive: the observer socket is off (observers.enabled = false)")
				}
				if len(res.Credentials) == 0 {
					fmt.Fprintln(a.out, "no observer credentials (hive observer create)")
					return nil
				}
				w := a.table()
				fmt.Fprintln(w, "ID\tNAME\tSTATE\tSCOPE\tSCREENS\tEXPIRES\tLAST USED\tCONNECTIONS")
				for _, c := range res.Credentials {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\n", c.ID, c.Name, c.State, c.Scope, yesNo(c.Screens),
						c.ExpiresAt.Local().Format(time.DateTime), lastUsed(c.LastUsedAt), c.Connections)
				}
				return w.Flush()
			})
		}),
	})
}

func lastUsed(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return t.Local().Format(time.DateTime)
}

func newObserverShowCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "show <id>",
		Short: "Show an observer credential",
		Args:  exactArgs(1),
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			var c runtime.ObserverInfo
			if err := a.client.Call(ctx, "observer.get", map[string]string{"id": args[0]}, &c); err != nil {
				return err
			}
			return a.emit(c, func() error {
				printCredential(a.out, c.Credential)
				fmt.Fprintf(a.out, "connections: %d\n", c.Connections)
				return nil
			})
		}),
	})
}

func printCredential(w io.Writer, c observer.Credential) {
	fmt.Fprintf(w, "id:          %s\nname:        %s\nstate:       %s\nscope:       %s\nscreens:     %s\n", c.ID, c.Name, c.State, c.Scope, yesNo(c.Screens))
	fmt.Fprintf(w, "created:     %s\nexpires:     %s\nlast used:   %s\n", c.CreatedAt.Local().Format(time.DateTime), c.ExpiresAt.Local().Format(time.DateTime), lastUsed(c.LastUsedAt))
	if c.RevokedAt != nil {
		fmt.Fprintf(w, "revoked:     %s\n", c.RevokedAt.Local().Format(time.DateTime))
	}
}

func newObserverRevokeCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke an observer credential; its open connections end at once",
		Args:  exactArgs(1),
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			var c observer.Credential
			if err := a.client.Call(ctx, "observer.revoke", map[string]string{"id": args[0]}, &c); err != nil {
				return err
			}
			return a.done(map[string]any{"credential": c}, "revoked observer %s (%s)", c.ID, c.Name)
		}),
	})
}

func newObserverValidateCmd(a *app) *cobra.Command {
	var tokenFile string
	cmd := needsDaemon(&cobra.Command{
		Use:   "validate",
		Short: "Check an observer token: what it is for, and whether it still works",
		Long: `Check the token in $` + observerTokenEnv + ` (or --token-file) against this session's
credentials without using it. Exits 1 when it is invalid, expired or revoked.`,
		Args: noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			token, err := a.observerToken(tokenFile)
			if err != nil {
				return err
			}
			var v runtime.ObserverValidation
			if err := a.client.Call(ctx, "observer.validate", map[string]string{"token": token}, &v); err != nil {
				return err
			}
			if err := a.emit(v, func() error {
				if v.Credential == nil {
					fmt.Fprintln(a.out, "invalid: no credential has this token")
					return nil
				}
				printCredential(a.out, *v.Credential)
				return nil
			}); err != nil {
				return err
			}
			if !v.Valid {
				return &codedError{code: exitError}
			}
			return nil
		}),
	})
	cmd.Flags().StringVar(&tokenFile, "token-file", "", "read the token from this file (- for standard input)")
	return cmd
}

func newObserverAuditCmd(a *app) *cobra.Command {
	var (
		credential string
		limit      int
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "audit",
		Short: "Show observer access: credentials made and revoked, connections, refusals",
		Args:  noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			var entries []observer.AuditEntry
			if err := a.client.Call(ctx, "observer.audit", map[string]any{"credential": credential, "limit": limit}, &entries); err != nil {
				return err
			}
			return a.emit(entries, func() error {
				w := a.table()
				fmt.Fprintln(w, "TIME\tACTION\tCREDENTIAL\tNAME\tDETAIL\tCLIENT")
				for _, e := range entries {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Time.Local().Format(time.DateTime), e.Action, dash(e.Credential), dash(e.Name), dash(e.Detail), dash(e.Client))
				}
				return w.Flush()
			})
		}),
	})
	cmd.Flags().StringVar(&credential, "credential", "", "only this credential's entries")
	cmd.Flags().IntVar(&limit, "limit", 100, "how many of the newest entries")
	return cmd
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// observerToken reads the token for commands that act as an observer.
func (a *app) observerToken(file string) (string, error) {
	var token string
	switch file {
	case "":
		token = a.getenv(observerTokenEnv)
	case "-":
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
		if err != nil {
			return "", err
		}
		token = string(b)
	default:
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		token = string(b)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", &usageError{fmt.Errorf("no observer token: set $%s or pass --token-file", observerTokenEnv)}
	}
	return token, nil
}

// observerFlags are what the commands acting as an observer take.
type observerFlags struct {
	tokenFile string
}

func (f *observerFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.tokenFile, "token-file", "", "read the token from this file (- for standard input; default $"+observerTokenEnv+")")
}

// connectObserver connects to this session's observer socket, or the
// remote one's through `hive proxy --observer`. It never starts a daemon.
func (a *app) connectObserver(ctx context.Context, f observerFlags) (*observer.Client, error) {
	token, err := a.observerToken(f.tokenFile)
	if err != nil {
		return nil, err
	}
	cfg := observer.ClientConfig{Socket: runtime.ObserverSocketPath(a.root), Token: token, Name: "hive-cli"}
	if a.remote != nil {
		cfg.Dial = a.remote.DialObserver
	}
	return observer.Connect(ctx, cfg)
}

func newObserverStatusCmd(a *app) *cobra.Command {
	var f observerFlags
	cmd := &cobra.Command{
		Use:   "status",
		Short: "As an observer: the daemon's health and what the token can see",
		Args:  noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			c, err := a.connectObserver(ctx, f)
			if err != nil {
				return err
			}
			defer c.Close()
			st, err := c.Status(ctx)
			if err != nil {
				return err
			}
			return a.emit(st, func() error {
				fmt.Fprintf(a.out, "daemon:       %s, session %s, up since %s\n", st.Version, st.Session, st.StartedAt.Local().Format(time.DateTime))
				fmt.Fprintf(a.out, "observer:     %s (%s), scope %s, screens %s, expires %s\n", st.Credential.ID, st.Credential.Name, st.Credential.Scope, yesNo(st.Credential.Screens), st.Credential.ExpiresAt.Local().Format(time.DateTime))
				fmt.Fprintf(a.out, "in scope:     %d environment(s), %d pane(s), %d agent(s)\n", st.Environments, st.Panes, st.Agents)
				fmt.Fprintf(a.out, "events:       epoch %s, at %d\n", st.Epoch, st.Seq)
				return nil
			})
		}),
	}
	f.add(cmd)
	return cmd
}

func newObserverSnapshotCmd(a *app) *cobra.Command {
	var f observerFlags
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "As an observer: print everything in scope as JSON",
		Args:  noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			c, err := a.connectObserver(ctx, f)
			if err != nil {
				return err
			}
			defer c.Close()
			snap, err := c.Snapshot(ctx)
			if err != nil {
				return err
			}
			if a.template != "" {
				return a.printTemplate(snap)
			}
			return a.printJSON(snap)
		}),
	}
	f.add(cmd)
	return cmd
}

func newObserverScreenCmd(a *app) *cobra.Command {
	var (
		f          observerFlags
		scrollback int
		ansi       bool
	)
	cmd := &cobra.Command{
		Use:   "screen <pane>",
		Short: "As an observer: print a pane's screen (needs --screens access)",
		Args:  exactArgs(1),
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			c, err := a.connectObserver(ctx, f)
			if err != nil {
				return err
			}
			defer c.Close()
			scr, err := c.Screen(ctx, observer.ScreenRequest{Pane: args[0], Scrollback: scrollback, ANSI: ansi})
			if err != nil {
				return err
			}
			return a.emit(scr, func() error {
				for _, l := range append(scr.Scrollback, scr.Lines...) {
					fmt.Fprintln(a.out, l)
				}
				return nil
			})
		}),
	}
	f.add(cmd)
	cmd.Flags().IntVar(&scrollback, "scrollback", 0, fmt.Sprintf("also print up to this many lines above the screen (at most %d)", observer.MaxScrollback))
	cmd.Flags().BoolVar(&ansi, "ansi", false, "keep colours and styles")
	return cmd
}

func newObserverEventsCmd(a *app) *cobra.Command {
	var (
		f     observerFlags
		epoch string
		since uint64
	)
	cmd := &cobra.Command{
		Use:   "events",
		Short: "As an observer: print events in scope as JSON lines until interrupted",
		Long: `Print the events this observer may see, one JSON object per line. With
--epoch and --since (an event's seq), events after it are replayed first when
the daemon still has them. An observer.resync event means events were missed:
take a new snapshot (hive observer snapshot) and continue.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := a.connectObserver(ctx, f)
			if err != nil {
				return err
			}
			defer c.Close()
			req := observer.EventsRequest{Epoch: epoch}
			if cmd.Flags().Changed("since") {
				req.Since = &since
			}
			stream, _, err := c.Events(ctx, req)
			if err != nil {
				return err
			}
			defer stream.Close()
			go func() { <-ctx.Done(); _ = stream.Close() }()
			return copyJSONLines(ctx, a.out, stream.Next)
		},
	}
	f.add(cmd)
	cmd.Flags().StringVar(&epoch, "epoch", "", "the event stream --since belongs to (from a snapshot or event)")
	cmd.Flags().Uint64Var(&since, "since", 0, "replay retained events after this seq first")
	return cmd
}

func newObserverWatchCmd(a *app) *cobra.Command {
	var (
		f        observerFlags
		interval time.Duration
		ansi     bool
	)
	cmd := &cobra.Command{
		Use:   "watch <pane>",
		Short: "As an observer: print a pane's screen updates as JSON lines (needs --screens access)",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := a.connectObserver(ctx, f)
			if err != nil {
				return err
			}
			defer c.Close()
			stream, _, err := c.Watch(ctx, observer.WatchRequest{Pane: args[0], ANSI: ansi, IntervalMS: int(interval / time.Millisecond)})
			if err != nil {
				return err
			}
			defer stream.Close()
			go func() { <-ctx.Done(); _ = stream.Close() }()
			return copyJSONLines(ctx, a.out, stream.Next)
		},
	}
	f.add(cmd)
	cmd.Flags().DurationVar(&interval, "interval", observer.DefaultWatchInterval, "at most one update per interval (100ms to 10s)")
	cmd.Flags().BoolVar(&ansi, "ansi", false, "keep colours and styles")
	return cmd
}

// copyJSONLines prints values from next as JSON lines until it ends.
func copyJSONLines[T any](ctx context.Context, out io.Writer, next func() (T, error)) error {
	enc := json.NewEncoder(out)
	for {
		v, err := next()
		if err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := enc.Encode(v); err != nil {
			return err
		}
	}
}
