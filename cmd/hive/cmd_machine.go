package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/discovery"
	"github.com/admirable-oss/hive/internal/remote"
)

// machineTimeout bounds one machine's connection, install included.
const machineTimeout = 2 * time.Minute

func newMachineCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "machine",
		Aliases: []string{"machines"},
		Short:   "Save other machines running Hive and work with them by name",
		Long: `Saved machines are other computers reached over SSH (your ~/.ssh/config,
keys and agent apply). Run any command on one with --machine <name>, and the
multiplexer's sidebar lists every enabled machine's agents beside this one's.
Connecting to a machine starts its daemon, and offers to install hive there
when it is missing or older than this one.

Machines on the local network can be found (discover) and paired (pair):
pairing pins the machine's identity, which is checked again by status, trust
and reconnect. A machine that proves another identity is not connected to
until you accept it (trust). See docs/discovery.md.`,
		Example: `  hive machine add gpu me@gpu-box.local
  hive machine discover
  hive machine pair studio
  hive --machine gpu agent list
  hive machine status`,
	}
	var (
		discover bool
		bf       browseFlags
	)
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List saved machines (and, with --discover, runtimes nearby)",
		Long: `List saved machines and their state: trusted (paired: its identity is pinned),
configured (saved without an identity), changed (it proved another identity:
not connected to until ` + "`hive machine trust`" + `), or disabled. With --discover,
the local network is asked too: saved machines found are marked nearby, and
runtimes not saved are listed as discovered. ` + "`hive machine status`" + ` connects
to them.`,
		Args: noArgs,
		RunE: withTimeout(machineTimeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			ms, err := a.machines().List()
			if err != nil {
				return err
			}
			var near []nearbyRuntime
			if discover {
				res, err := a.browse(ctx, bf)
				if err != nil {
					return err
				}
				near = a.nearby(ctx, res)
				if ms, err = a.machines().List(); err != nil { // addresses may have moved
					return err
				}
			}
			entries := machineEntries(ms, near)
			return a.emit(entries, func() error {
				w := a.table()
				fmt.Fprintln(w, "NAME\tHOST\tSESSION\tSTATE\tIDENTITY")
				for _, e := range entries {
					state, id := e.State, discovery.ShortID(e.ID)
					if e.Announcement != nil {
						id = discovery.ShortID(e.Announcement.ID) + " (announced)"
					}
					if e.Nearby && e.Announcement == nil {
						state += ", nearby"
					}
					session := cmpStr(e.Session, "default")
					if e.Announcement != nil {
						session = "-"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", e.Name, e.Host, session, state, cmpStr(id, "-"))
				}
				return w.Flush()
			})
		}),
	}
	list.Flags().BoolVar(&discover, "discover", false, "ask the local network for runtimes too")
	bf.add(list)
	enable := func(on bool) *cobra.Command {
		use, short := "enable <name>", "Connect to a machine again (and list its agents in the UI)"
		if !on {
			use, short = "disable <name>", "Keep a machine saved without connecting to it"
		}
		return &cobra.Command{
			Use:   use,
			Short: short,
			Args:  exactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				m, err := a.machines().Update(args[0], func(m *remote.Machine) error { m.Disabled = !on; return nil })
				if err != nil {
					return err
				}
				state := "enabled"
				if !on {
					state = "disabled"
				}
				return a.done(map[string]any{"machine": m}, "%s machine %q", state, m.Name)
			},
		}
	}
	cmd.AddCommand(newMachineAddCmd(a), list,
		newMachineDiscoverCmd(a), newMachinePairCmd(a), newMachineTrustCmd(a), newMachineRevokeCmd(a), newMachineIdentityCmd(a), newMachineProxyCmd(a),
		&cobra.Command{
			Use:   "rename <name> <new-name>",
			Short: "Rename a saved machine",
			Args:  exactArgs(2),
			RunE: func(_ *cobra.Command, args []string) error {
				m, err := a.machines().Update(args[0], func(m *remote.Machine) error { m.Name = args[1]; return nil })
				if err != nil {
					return err
				}
				return a.done(map[string]any{"machine": m}, "renamed machine %q to %q", args[0], m.Name)
			},
		},
		enable(true), enable(false),
		&cobra.Command{
			Use:     "remove <name>",
			Aliases: []string{"rm"},
			Short:   "Forget a machine (its daemon and agents keep running there)",
			Args:    exactArgs(1),
			RunE: withTimeout(30*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
				m, err := a.machines().Get(args[0])
				if err != nil {
					return err
				}
				if err := a.machines().Remove(m.Name); err != nil {
					return err
				}
				_ = a.target(m.Target("")).CloseShared(ctx)
				return a.done(map[string]any{"machine": m.Name}, "removed machine %q", m.Name)
			}),
		},
		newMachineStatusCmd(a),
		&cobra.Command{
			Use:   "reconnect <name>",
			Short: "Drop a machine's SSH connection and connect afresh",
			Args:  exactArgs(1),
			RunE: withTimeout(machineTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
				m, err := a.machines().Get(args[0])
				if err != nil {
					return err
				}
				t := a.target(m.Target(""))
				if err := t.CloseShared(ctx); err != nil {
					return err
				}
				st := machineStatus(ctx, a, m)
				if st.Error != "" {
					return fmt.Errorf("reconnect to %s: %s", m.Name, st.Error)
				}
				return a.emit(st, func() error { return printMachineStatus(a, []machineState{st}) })
			}),
		},
	)
	return cmd
}

func newMachineAddCmd(a *app) *cobra.Command {
	var (
		m       remote.Machine
		noCheck bool
	)
	cmd := &cobra.Command{
		Use:   "add <name> <host>",
		Short: "Save a machine (an ssh destination), after checking it can be reached",
		Long: `Save a machine under a name. host is anything ssh accepts: user@host, or a
Host from ~/.ssh/config. Unless --no-check is given, Hive connects first,
starting the daemon there, and offers to install hive when it is missing or
older than this one (--yes installs without asking).`,
		Example: `  hive machine add gpu me@gpu-box.local
  hive machine add ci ci-runner --session agents --hive-path /opt/hive/bin/hive`,
		Args: exactArgs(2),
		RunE: withTimeout(machineTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			m.Name, m.Host = args[0], args[1]
			if err := remote.ValidName(m.Name); err != nil {
				return &usageError{err}
			}
			if _, err := a.machines().Get(m.Name); err == nil {
				return fmt.Errorf("%w: %q", remote.ErrMachineExists, m.Name)
			}
			if !noCheck {
				t := a.target(m.Target(""))
				b := *a
				b.remote, b.remoteName, b.client = &t, m.Name, a.remoteClient(t, "cli")
				defer b.client.Close()
				if err := b.ensureRemote(ctx); err != nil {
					return fmt.Errorf("%w (add it anyway with --no-check)", err)
				}
			}
			saved, err := a.machines().Add(m)
			if err != nil {
				return err
			}
			return a.done(map[string]any{"machine": saved}, "added machine %q (%s); use it with --machine %s", saved.Name, saved.Host, saved.Name)
		}),
	}
	cmd.Flags().StringVar(&m.Session, "session", "", "the session to use there (default: its default session)")
	cmd.Flags().StringVar(&m.HivePath, "hive-path", "", "where hive is there (default: on its PATH, else ~/.local/bin/hive)")
	cmd.Flags().BoolVar(&noCheck, "no-check", false, "save it without connecting")
	return cmd
}

// machineState is one machine's health, as `machine status` reports it.
type machineState struct {
	remote.Machine
	Reachable bool    `json:"reachable"`
	RTTMS     float64 `json:"rtt_ms,omitempty"`
	Version   string  `json:"version,omitempty"`
	APILevel  int     `json:"api_level,omitempty"`
	DaemonPID int     `json:"daemon_pid,omitempty"`
	Agents    int     `json:"agents"`
	// State is the most urgent of its agents' states (blocked, working, …).
	State string `json:"state,omitempty"`
	// Identity is what its identity proof says: verified (the pinned
	// one), changed (another), unpinned (none is pinned), or unverified.
	Identity   string `json:"identity,omitempty"`
	IdentityID string `json:"identity_id,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Identity verdicts of `machine status`.
const (
	identityVerified   = "verified"
	identityChanged    = "changed"
	identityUnpinned   = "unpinned"
	identityUnverified = "unverified"
)

func newMachineStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status [name...]",
		Short: "Connect to machines and show their daemons, latency and agents",
		Long: `Connect to every enabled machine (or the named ones) and show whether it is
reachable, its round-trip time, its hive version and its agents. Connecting
starts a machine's daemon when it is not running.`,
		RunE: withTimeout(machineTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			all, err := a.machines().List()
			if err != nil {
				return err
			}
			var ms []remote.Machine
			for _, m := range all {
				if len(args) == 0 && !m.Disabled {
					ms = append(ms, m)
				}
			}
			for _, name := range args {
				m, err := a.machines().Get(name)
				if err != nil {
					return err
				}
				ms = append(ms, m)
			}
			states := make([]machineState, len(ms))
			var wg sync.WaitGroup
			for i, m := range ms {
				wg.Go(func() { states[i] = machineStatus(ctx, a, m) })
			}
			wg.Wait()
			return a.emit(states, func() error {
				if len(states) == 0 {
					_, err := fmt.Fprintln(a.out, "no machines (add one with `hive machine add <name> <host>`)")
					return err
				}
				return printMachineStatus(a, states)
			})
		}),
	}
}

// machineStatus connects to m and reads its state.
func machineStatus(ctx context.Context, a *app, m remote.Machine) machineState {
	st := machineState{Machine: m}
	c := a.remoteClient(a.target(m.Target("")), "cli")
	defer c.Close()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := c.Ping(ctx); err != nil { // connects
		st.Error = err.Error()
		return st
	}
	start := time.Now()
	if err := c.Ping(ctx); err != nil {
		st.Error = err.Error()
		return st
	}
	st.Reachable, st.RTTMS = true, float64(time.Since(start).Microseconds())/1000
	if ds, err := c.Status(ctx); err == nil {
		st.Version, st.APILevel, st.DaemonPID = ds.Version, ds.APILevel, ds.PID
	}
	if res, err := client.NewAgents(c).List(ctx, false); err == nil {
		st.Agents, st.State = len(res.Agents), string(res.Rollups.Session)
	}
	st.Identity = identityUnverified
	p, err := a.target(m.Target("")).Identify(ctx)
	switch {
	case err != nil:
	case m.ID == "":
		st.Identity, st.IdentityID = identityUnpinned, p.ID
	case p.ID == m.ID:
		st.Identity, st.IdentityID = identityVerified, p.ID
	default:
		st.Identity, st.IdentityID = identityChanged, p.ID
		if saved, err := a.machines().Update(m.Name, func(x *remote.Machine) error { x.IdentityChanged = true; return nil }); err == nil {
			st.Machine = saved
		}
	}
	return st
}

func printMachineStatus(a *app, states []machineState) error {
	w := a.table()
	fmt.Fprintln(w, "NAME\tHOST\tSTATUS\tRTT\tVERSION\tAGENTS\tIDENTITY")
	for _, s := range states {
		status, rtt := "reachable", fmt.Sprintf("%.1f ms", s.RTTMS)
		if !s.Reachable {
			status, rtt = "unreachable: "+s.Error, "-"
		}
		agents := fmt.Sprint(s.Agents)
		if s.State != "" {
			agents += " (" + s.State + ")"
		}
		identity := cmpStr(s.Identity, "-")
		if s.Identity == identityChanged {
			identity = "CHANGED (hive machine trust " + s.Name + ")"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Name, s.Host, status, rtt, cmpStr(s.Version, "-"), agents, identity)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	for _, s := range states {
		if s.Identity == identityChanged {
			fmt.Fprintf(a.errOut, "hive: machine %q proved another identity (%s) than the one it was paired with; it is not connected to until `hive machine trust %s` accepts it\n",
				s.Name, discovery.ShortID(s.IdentityID), s.Name)
		}
	}
	return nil
}
