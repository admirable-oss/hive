package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/observer"
	"github.com/admirable-oss/hive/internal/runtime"
	"github.com/admirable-oss/hive/internal/web"
)

func newWebCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Show your agents in a browser, read-only (the web observer)",
		Long: `The web observer shows agents, their states, screens and activity in a
browser, live, from any number of Hive runtimes (a session on a machine). It
cannot type into terminals, change layouts or start anything: it reaches each
runtime only through that runtime's observer socket.

  1. hive web runtime add laptop --all --screens      # allow it to observe this session
  2. hive web serve                                   # run it (127.0.0.1:8484)
  3. hive web share --runtime laptop --screens        # a one-time sign-in link

Links carry a one-time code that a browser exchanges for a session cookie;
access expires, and ` + "`hive web revoke`" + ` ends it at once. See docs/web.md.`,
	}
	runtimeCmd := &cobra.Command{Use: "runtime", Short: "Choose which Hive runtimes the web observer may show"}
	runtimeCmd.AddCommand(newWebRuntimeAddCmd(a), newWebRuntimeListCmd(a), newWebRuntimeRemoveCmd(a))
	cmd.AddCommand(
		newWebServeCmd(a),
		runtimeCmd,
		newWebShareCmd(a),
		newWebGrantsCmd(a),
		newWebRevokeCmd(a),
		newWebAuditCmd(a),
		newWebStatusCmd(a),
		newWebPushCmd(a),
	)
	return cmd
}

func newWebPushCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "push", Short: "Notifications on phones and browsers (Web Push)"}
	status := &cobra.Command{
		Use:   "status",
		Short: "Show the devices that get notifications, and their deliveries",
		Args:  noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			var st web.PushStatus
			if err := a.webAdmin(ctx, "web.push.status", nil, &st); err != nil {
				return err
			}
			return a.emit(st, func() error {
				if !st.Enabled {
					fmt.Fprintln(a.out, "notifications are off (web.push = false)")
					return nil
				}
				fmt.Fprintf(a.out, "notifications on; VAPID public key %s\n", st.PublicKey)
				for rt, ready := range st.Runtimes {
					state := "connecting"
					if ready {
						state = "watching"
					}
					fmt.Fprintf(a.out, "runtime %s: %s\n", rt, state)
				}
				if len(st.Devices) == 0 {
					fmt.Fprintln(a.out, "no devices (turn notifications on in Settings, in a browser or the installed app)")
					return nil
				}
				w := a.table()
				fmt.Fprintln(w, "DEVICE\tSESSION\tPUSH SERVICE\tSINCE\tLAST DELIVERED\tFAILURES\tLAST ERROR")
				for _, d := range st.Devices {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n", d.ID, d.Session, hostOnly(d.Endpoint), d.CreatedAt.Local().Format(time.DateTime), lastUsed(d.LastOK), d.Failures, dash(d.LastError))
				}
				return w.Flush()
			})
		}),
	}
	test := &cobra.Command{
		Use:   "test <grant-or-session>",
		Short: "Send a test notification to the devices of a grant or a browser session",
		Args:  exactArgs(1),
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			var res map[string]int
			if err := a.webAdmin(ctx, "web.push.test", map[string]string{"id": args[0]}, &res); err != nil {
				return err
			}
			return a.done(map[string]any{"devices": res["devices"]}, "sent a test notification to %d device(s)", res["devices"])
		}),
	}
	cmd.AddCommand(status, test)
	return cmd
}

func hostOnly(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil {
		return u.Host
	}
	return endpoint
}

// webDir holds the web observer's state; it serves every session, so it
// lives beside them.
func (a *app) webDir() string { return filepath.Join(a.base, "web") }

// webAdmin calls a running web observer.
func (a *app) webAdmin(ctx context.Context, method string, params, result any) error {
	c := client.NewService(client.Config{SocketPath: web.AdminSocketPath(a.webDir()), Name: "hive-web-cli"})
	defer c.Close()
	err := c.Call(ctx, method, params, result)
	if errors.Is(err, client.ErrUnavailable) {
		return errors.New("the web observer is not running; start it with `hive web serve`")
	}
	return err
}

func newWebServeCmd(a *app) *cobra.Command {
	var (
		listen, publicURL, cert, key string
		behindProxy                  bool
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the web observer in the foreground",
		Long: `Serve the web observer until interrupted. By default it listens on
127.0.0.1:8484, this machine only. Listening on the network needs TLS
(--tls-cert, --tls-key) or a reverse proxy that terminates it (--behind-proxy).
Settings come from [web] in the config file; flags override them.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := a.cfg.Web
			flags := cmd.Flags()
			pick := func(name string, flag *string, dflt string) string {
				if flags.Changed(name) {
					return *flag
				}
				return dflt
			}
			log, closeLog, err := logging.New(a.cfg.LoggingConfig(filepath.Join(a.webDir(), "web.log"), true))
			if err != nil {
				return err
			}
			defer func() { _ = closeLog() }()
			srv, err := web.New(web.Config{
				Dir:         a.webDir(),
				Listen:      pick("listen", &listen, cfg.Listen),
				PublicURL:   pick("public-url", &publicURL, cfg.PublicURL),
				TLSCert:     pick("tls-cert", &cert, cfg.TLSCert),
				TLSKey:      pick("tls-key", &key, cfg.TLSKey),
				BehindProxy: behindProxy || cfg.BehindProxy,
				Logger:      log,
				Version:     buildinfo.Get().Version,
				Push:        web.PushConfig{Disabled: !cfg.Push, Contact: cfg.PushContact, AllowAnyEndpoint: cfg.PushAllowAnyEndpoint},
			})
			if errors.Is(err, web.ErrInsecure) {
				return &usageError{err}
			}
			if err != nil {
				return err
			}
			st := srv.Status()
			fmt.Fprintf(a.errOut, "hive: web observer on %s (%d runtime(s)); share a link with `hive web share`\n", st.PublicURL, len(st.Runtimes))
			if len(st.Runtimes) == 0 {
				fmt.Fprintln(a.errOut, "hive: no runtimes yet: allow one with `hive web runtime add <name> --all`")
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGTERM)
			defer stop()
			return srv.Run(ctx)
		},
	}
	f := cmd.Flags()
	f.StringVar(&listen, "listen", web.DefaultListen, "HTTP address (host:port)")
	f.StringVar(&publicURL, "public-url", "", "where browsers reach it, for share links")
	f.StringVar(&cert, "tls-cert", "", "TLS certificate file (PEM)")
	f.StringVar(&key, "tls-key", "", "TLS private key file (PEM)")
	f.BoolVar(&behindProxy, "behind-proxy", false, "a reverse proxy in front terminates TLS")
	return cmd
}

func newWebRuntimeAddCmd(a *app) *cobra.Command {
	var (
		envs, panes []string
		all         bool
		screens     bool
		expires     time.Duration
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "add <name>",
		Short: "Allow the web observer to show this Hive runtime (or the --remote one)",
		Long: `Make an observer credential on the runtime this command reaches (this
session, or --session, --remote, --machine) for the web observer, and keep its
token in the web observer's state. Nothing is shown in a browser until you
share a link (hive web share). Adding a name again replaces it.`,
		Example: `  hive web runtime add laptop --all --screens
  hive --remote buildbox web runtime add buildbox --env ci --screens`,
		Args: exactArgs(1),
		RunE: withTimeout(30*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			name := args[0]
			if !web.ValidRuntimeName(name) {
				return &usageError{errors.New("a runtime name is lowercase letters, digits, '.', '_' and '-' (up to 32)")}
			}
			scope := observer.Scope{All: all, Environments: envs, Panes: panes}
			if scope.Validate() != nil {
				return &usageError{errors.New("give --all, or --env and --pane for what the web observer may see")}
			}
			var created runtime.ObserverCreated
			if err := a.client.Call(ctx, "observer.create", runtime.ObserverCreateParams{
				Name: "hive-web:" + name, Scope: scope, Screens: screens, TTLSeconds: int64(expires / time.Second),
			}, &created); err != nil {
				return err
			}
			store := web.NewStore(a.webDir())
			tokenFile, err := store.WriteToken(name, created.Token)
			if err != nil {
				return err
			}
			rt := web.Runtime{
				Name: name, Session: a.session, Credential: created.Credential.ID, TokenFile: tokenFile,
				Scope: created.Credential.Scope, Screens: screens, ExpiresAt: created.Credential.ExpiresAt, AddedAt: time.Now().UTC(),
			}
			if a.remote != nil {
				rt.Host, rt.Session, rt.SSH, rt.HivePath = a.remote.Host, a.remote.Session, a.remote.SSH, a.remote.HivePath
			} else {
				rt.Socket = created.Socket
			}
			if err := store.SaveRuntime(rt); err != nil {
				return err
			}
			reloaded := a.webAdmin(ctx, "web.reload", nil, nil) == nil
			return a.emit(rt, func() error {
				fmt.Fprintf(a.out, "the web observer may show %s (%s): scope %s, screens %s, until %s\n", name, rt.Where(), scopeLabel(rt.Scope), yesNo(screens), rt.ExpiresAt.Local().Format(time.DateTime))
				if !reloaded {
					fmt.Fprintln(a.out, "start it with `hive web serve`, then share a link with `hive web share --runtime "+name+"`")
				}
				return nil
			})
		}),
	})
	cmd.Flags().StringArrayVar(&envs, "env", nil, "an environment it may show (repeatable)")
	cmd.Flags().StringArrayVar(&panes, "pane", nil, "a pane it may show (repeatable)")
	cmd.Flags().BoolVar(&all, "all", false, "it may show every environment of the session")
	cmd.Flags().BoolVar(&screens, "screens", false, "it may show screen contents and output (each link still has to allow them)")
	cmd.Flags().DurationVar(&expires, "expires", 90*24*time.Hour, "how long its credential lasts (up to 8760h)")
	return cmd
}

func scopeLabel(s observer.Scope) string {
	if s.All {
		return "all"
	}
	return s.String()
}

func newWebRuntimeListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the runtimes the web observer may show",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := web.NewStore(a.webDir()).Runtimes()
			if err != nil {
				return err
			}
			if list == nil {
				list = []web.Runtime{}
			}
			return a.emit(list, func() error {
				if len(list) == 0 {
					fmt.Fprintln(a.out, "no runtimes (hive web runtime add)")
					return nil
				}
				w := a.table()
				fmt.Fprintln(w, "NAME\tWHERE\tSCOPE\tSCREENS\tCREDENTIAL\tUNTIL")
				for _, r := range list {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, r.Where(), scopeLabel(r.Scope), yesNo(r.Screens), r.Credential, r.ExpiresAt.Local().Format(time.DateTime))
				}
				return w.Flush()
			})
		},
	}
}

func newWebRuntimeRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Stop showing a runtime; its viewers lose it at once",
		Args:    exactArgs(1),
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			rt, err := web.NewStore(a.webDir()).RemoveRuntime(args[0])
			if err != nil {
				return err
			}
			_ = a.webAdmin(ctx, "web.reload", nil, nil)
			// Its credential is revoked where this command can reach it.
			revoked := false
			if rt.Host == "" && rt.Socket == runtime.ObserverSocketPath(a.root) {
				revoked = a.client.Call(ctx, "observer.revoke", map[string]string{"id": rt.Credential}, nil) == nil
			}
			return a.done(map[string]any{"runtime": rt.Name, "credential_revoked": revoked}, "%s", removedText(rt, revoked))
		}),
	}
}

func removedText(rt web.Runtime, revoked bool) string {
	text := "the web observer no longer shows " + rt.Name
	if !revoked {
		where := ""
		if rt.Host != "" {
			where = "--remote " + rt.Host + " "
		}
		text += fmt.Sprintf("; revoke its credential with `hive %sobserver revoke %s`", where, rt.Credential)
	}
	return text
}

func newWebShareCmd(a *app) *cobra.Command {
	var (
		runtimes, envs, panes []string
		all, screens, history bool
		expires, linkExpires  time.Duration
		uses                  int
		label                 string
	)
	cmd := &cobra.Command{
		Use:   "share",
		Short: "Make a one-time sign-in link for a browser",
		Long: `Print a link that signs one browser (or --uses of them) in to the web
observer. The link works once, for --link-expires; the access it gives lasts
--expires, or until ` + "`hive web revoke`" + `. Screens and output history are
shown only when the link allows them (--screens, --history) and the runtime
does too.`,
		Example: `  hive web share --runtime laptop
  hive web share --runtime laptop --runtime buildbox --env api --screens --expires 2h
  hive web share --runtime laptop --pane p7 --screens --history --uses 3 --label "pairing with Sam"`,
		Args: noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			if len(runtimes) == 0 {
				list, err := web.NewStore(a.webDir()).Runtimes()
				if err != nil {
					return err
				}
				if len(list) != 1 {
					return &usageError{errors.New("name the runtimes it shows with --runtime (hive web runtime list)")}
				}
				runtimes = []string{list[0].Name}
			}
			scope := observer.Scope{All: all || (len(envs) == 0 && len(panes) == 0), Environments: envs, Panes: panes}
			var res web.Shared
			if err := a.webAdmin(ctx, "web.share", web.ShareRequest{
				Label: label, Runtimes: runtimes, Scope: scope, Screens: screens || history, History: history,
				TTLSeconds: int64(expires / time.Second), LinkTTLSeconds: int64(linkExpires / time.Second), MaxUses: uses,
			}, &res); err != nil {
				return err
			}
			return a.emit(res, func() error {
				g := res.Grant
				fmt.Fprintf(a.out, "%s\n\n", res.Link)
				fmt.Fprintf(a.out, "grant %s (%s): %s, scope %s, screens %s, history %s\n", g.ID, g.Label, strings.Join(g.Runtimes, ", "), scopeLabel(g.Scope), yesNo(g.Screens), yesNo(g.History))
				fmt.Fprintf(a.out, "the link works %d time(s) until %s; access lasts until %s (hive web revoke %s ends it)\n",
					g.MaxUses, g.LinkExpiresAt.Local().Format(time.TimeOnly), g.ExpiresAt.Local().Format(time.DateTime), g.ID)
				return nil
			})
		}),
	}
	f := cmd.Flags()
	f.StringArrayVar(&runtimes, "runtime", nil, "a runtime it shows (repeatable; default: the only one)")
	f.StringArrayVar(&envs, "env", nil, "only this environment (repeatable)")
	f.StringArrayVar(&panes, "pane", nil, "only this pane (repeatable)")
	f.BoolVar(&all, "all", false, "everything the runtimes show (the default without --env or --pane)")
	f.BoolVar(&screens, "screens", false, "show screen contents")
	f.BoolVar(&history, "history", false, "also allow reading output history (implies --screens)")
	f.DurationVar(&expires, "expires", web.DefaultGrantTTL, "how long the access lasts (1m to 720h)")
	f.DurationVar(&linkExpires, "link-expires", web.DefaultLinkTTL, "how long the link can be opened")
	f.IntVar(&uses, "uses", 1, fmt.Sprintf("how many browsers may sign in with it (up to %d)", web.MaxLinkUses))
	f.StringVar(&label, "label", "", "a name for it, shown to the viewer and in the audit log")
	return cmd
}

func newWebGrantsCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "grants",
		Aliases: []string{"list"},
		Short:   "List shared access and the browsers signed in with it",
		Args:    noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			var res web.GrantList
			if err := a.webAdmin(ctx, "web.grants", nil, &res); err != nil {
				return err
			}
			return a.emit(res, func() error {
				if len(res.Grants) == 0 {
					fmt.Fprintln(a.out, "nothing shared (hive web share)")
					return nil
				}
				w := a.table()
				fmt.Fprintln(w, "ID\tLABEL\tSTATE\tRUNTIMES\tSCOPE\tSCREENS\tHISTORY\tUNTIL\tBROWSERS")
				for _, g := range res.Grants {
					n := 0
					for _, s := range res.Sessions {
						if s.Grant == g.ID {
							n++
						}
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\n", g.ID, g.Label, g.State, strings.Join(g.Runtimes, ","), scopeLabel(g.Scope),
						yesNo(g.Screens), yesNo(g.History), g.ExpiresAt.Local().Format(time.DateTime), n)
				}
				if err := w.Flush(); err != nil {
					return err
				}
				if len(res.Sessions) > 0 {
					fmt.Fprintln(a.out)
					w = a.table()
					fmt.Fprintln(w, "SESSION\tGRANT\tSIGNED IN\tLAST SEEN\tNOTIFIED DEVICES\tBROWSER")
					for _, s := range res.Sessions {
						fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n", s.ID, s.Grant, s.CreatedAt.Local().Format(time.DateTime), s.LastSeenAt.Local().Format(time.DateTime), res.Devices[s.ID], s.Client)
					}
					return w.Flush()
				}
				return nil
			})
		}),
	}
}

func newWebRevokeCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <grant-or-session>",
		Short: "End shared access (a grant and its browsers, or one browser) at once",
		Args:  exactArgs(1),
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			var res web.Revoked
			if err := a.webAdmin(ctx, "web.revoke", map[string]string{"id": args[0]}, &res); err != nil {
				return err
			}
			return a.done(map[string]any{"grant": res.Grant, "sessions": res.Sessions}, "revoked %s: %d browser(s) signed out", args[0], len(res.Sessions))
		}),
	}
}

func newWebAuditCmd(a *app) *cobra.Command {
	var (
		grant string
		limit int
	)
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Show web access: links shared and used, sign-ins, history read, revocations",
		Args:  noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			var entries []observer.AuditEntry
			if err := a.webAdmin(ctx, "web.audit", map[string]any{"grant": grant, "limit": limit}, &entries); err != nil {
				return err
			}
			return a.emit(entries, func() error {
				w := a.table()
				fmt.Fprintln(w, "TIME\tACTION\tGRANT\tLABEL\tDETAIL\tCLIENT")
				for _, e := range entries {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Time.Local().Format(time.DateTime), e.Action, dash(e.Credential), dash(e.Name), dash(e.Detail), dash(e.Client))
				}
				return w.Flush()
			})
		}),
	}
	cmd.Flags().StringVar(&grant, "grant", "", "only this grant's entries")
	cmd.Flags().IntVar(&limit, "limit", 100, "how many of the newest entries")
	return cmd
}

func newWebStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the running web observer and its runtimes' connections",
		Args:  noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			var st web.Status
			if err := a.webAdmin(ctx, "web.status", nil, &st); err != nil {
				return err
			}
			return a.emit(st, func() error {
				fmt.Fprintf(a.out, "web observer %s at %s (listening on %s), up since %s, %d page(s) open\n", st.Version, st.PublicURL, st.Listen, st.StartedAt.Local().Format(time.DateTime), st.Viewers)
				w := a.table()
				fmt.Fprintln(w, "RUNTIME\tWHERE\tCONNECTION\tSYNCED\tERROR")
				for _, r := range st.Runtimes {
					synced := "never"
					if !r.SyncedAt.IsZero() {
						synced = r.SyncedAt.Local().Format(time.DateTime)
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.Name, r.Where, r.State, synced, dash(r.Error))
				}
				return w.Flush()
			})
		}),
	}
}
