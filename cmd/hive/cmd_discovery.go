package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/discovery"
	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/remote"
)

// Local discovery (M17): runtimes announce themselves on the local network
// when they opt in, and are paired by proving their identity over ssh.
// Nothing found by discovery is trusted, saved or connected to without
// that proof and the user's confirmation.

// identity is this installation's runtime identity, shared by every session.
func (a *app) identity() (*discovery.Identity, error) { return discovery.LoadIdentity(a.base) }

// discoveryName is the name this machine is announced under.
func (a *app) discoveryName() string {
	if n := discovery.InstanceName(a.cfg.Discovery.Name); n != "" {
		return n
	}
	return discovery.DefaultName()
}

func platformName() string { return goruntime.GOOS + "/" + goruntime.GOARCH }

// advertise announces this machine while the daemon runs, when
// discovery.advertise is on. One daemon per installation announces it
// (sessions share the identity); the others leave it be. The returned
// function stops it, saying goodbye.
func (a *app) advertise(ctx context.Context, log *slog.Logger) (stop func()) {
	if !a.cfg.Discovery.Advertise {
		return func() {}
	}
	if goruntime.GOOS == "windows" {
		// Other machines cannot reach Hive here over ssh yet (it is a
		// remote client only), so there is nothing to pair with.
		log.Warn("discovery: not advertising: Windows cannot be a remote machine yet (docs/platforms.md)")
		return func() {}
	}
	lock, err := os.OpenFile(filepath.Join(a.base, "discovery.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		log.Warn("discovery: cannot advertise", "err", err)
		return func() {}
	}
	if ok, err := platform.TryLock(lock); err != nil || !ok {
		_ = lock.Close()
		log.Info("discovery: another daemon advertises this machine")
		return func() {}
	}
	id, err := a.identity()
	if err != nil {
		_ = lock.Close()
		log.Warn("discovery: cannot advertise", "err", err)
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() {
		err := discovery.Advertise(ctx, discovery.ResponderConfig{
			Advertisement: discovery.Advertisement{
				Instance: a.discoveryName(), ID: id.ID, Port: a.cfg.Discovery.SSHPort,
				APILevel: protocol.APILevel, Caps: []string{discovery.CapSSH},
			},
			Interfaces: a.cfg.Discovery.Interfaces,
			Addr:       a.getenv(discovery.EnvAddr),
			Logger:     log,
		})
		if err != nil {
			log.Warn("discovery: cannot advertise", "err", err)
		}
	})
	return func() {
		cancel()
		wg.Wait()
		_ = platform.Unlock(lock)
		_ = lock.Close()
	}
}

func newMachineIdentityCmd(a *app) *cobra.Command {
	var challenge string
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Show this machine's runtime identity, which pairing pins",
		Long: `Every Hive installation has an identity: a key pair made on first use and
kept in <HIVE_HOME>/identity.json. Pairing pins it, so another machine can tell
this one from any other at the same address or with the same name. Compare the
fingerprint shown here with the one ` + "`hive machine pair`" + ` shows elsewhere.

--prove signs a challenge with it; that is how pairing checks it over ssh.`,
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			id, err := a.identity()
			if err != nil {
				return err
			}
			host, _ := os.Hostname()
			if challenge != "" {
				p, err := id.Prove(challenge, a.discoveryName(), host, platformName(), buildinfo.Get().Version)
				if err != nil {
					return &usageError{err}
				}
				return a.printJSON(p)
			}
			v := map[string]any{
				"id": id.ID, "fingerprint": discovery.Fingerprint(id.PublicKey), "name": a.discoveryName(),
				"hostname": host, "platform": platformName(), "advertise": a.cfg.Discovery.Advertise,
			}
			return a.emit(v, func() error {
				adv := "no (discovery.advertise is off)"
				if a.cfg.Discovery.Advertise {
					adv = fmt.Sprintf("yes, on ssh port %d", a.cfg.Discovery.SSHPort)
				}
				w := a.table()
				fmt.Fprintf(w, "name:\t%s\nid:\t%s\nfingerprint:\t%s\nplatform:\t%s\nadvertised:\t%s\n",
					a.discoveryName(), id.ID, discovery.Fingerprint(id.PublicKey), platformName(), adv)
				return w.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&challenge, "prove", "", "sign this challenge (hex) and print the proof as JSON")
	return cmd
}

// browseFlags are how discover, list --discover and pair look.
type browseFlags struct {
	timeout    time.Duration
	interfaces []string
	addresses  []string
}

func (f *browseFlags) add(cmd *cobra.Command) {
	cmd.Flags().DurationVar(&f.timeout, "timeout", discovery.DefaultTimeout, "how long to wait for answers")
	cmd.Flags().StringSliceVar(&f.interfaces, "interface", nil, "look on this network interface only (repeatable; default: discovery.interfaces)")
	cmd.Flags().StringSliceVar(&f.addresses, "address", nil, "ask this address directly, for networks without multicast (host[:port], repeatable)")
}

// errBrowseOff is returned when discovery.browse is off.
var errBrowseOff = errors.New("discovery is off (discovery.browse = false); ask a machine directly with --address, or pair with --host")

func (a *app) browse(ctx context.Context, f browseFlags) (discovery.Result, error) {
	cfg := discovery.BrowseConfig{Timeout: f.timeout, Interfaces: f.interfaces, Addrs: f.addresses}
	if len(cfg.Interfaces) == 0 {
		cfg.Interfaces = a.cfg.Discovery.Interfaces
	}
	if !a.cfg.Discovery.Browse {
		if len(f.addresses) == 0 {
			return discovery.Result{}, errBrowseOff
		}
		cfg.NoMulticast = true // asking named addresses is not browsing
	}
	return discovery.Browse(ctx, cfg)
}

// nearbyRuntime is a runtime discovery found, with what this machine knows
// of it.
type nearbyRuntime struct {
	discovery.Record
	// State is the saved machine's state, or discovered when unsaved.
	State string `json:"state"`
	// Machine is the saved machine with its identity.
	Machine string `json:"machine,omitempty"`
	// Note says what was done or noticed (an address change, say).
	Note string `json:"note,omitempty"`
}

// nearby matches what a browse found with the saved machines by identity.
// A discovered machine seen at a new address gets it, once it proves its
// identity there: an announcement alone never moves a machine.
func (a *app) nearby(ctx context.Context, res discovery.Result) []nearbyRuntime {
	out := make([]nearbyRuntime, 0, len(res.Runtimes))
	for _, rec := range res.Runtimes {
		n := nearbyRuntime{Record: rec, State: remote.StateDiscovered}
		m, ok, _ := a.machines().FindID(rec.ID)
		if ok {
			n.State, n.Machine = m.State(), m.Name
			n.Note = a.follow(ctx, m, rec)
		}
		out = append(out, n)
	}
	return out
}

// follow updates a discovered machine's address when rec has it elsewhere,
// after it proves its identity at the new one.
func (a *app) follow(ctx context.Context, m remote.Machine, rec discovery.Record) string {
	dest, ok := rec.SSHDestination(sshUser(m.Host))
	if !m.Discovered || !ok || dest == m.Host || m.IdentityChanged {
		return ""
	}
	if m.Disabled {
		return "seen at " + dest + " (disabled, so not followed)"
	}
	moved := m
	moved.Host = dest
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	p, err := a.target(moved.Target("")).Identify(ctx)
	switch {
	case err != nil:
		return fmt.Sprintf("seen at %s, but it did not prove its identity there: %v", dest, err)
	case p.ID != m.ID:
		return fmt.Sprintf("seen at %s, but a different identity answered there (%s)", dest, discovery.ShortID(p.ID))
	}
	if _, err := a.machines().Update(m.Name, func(x *remote.Machine) error { x.Host = dest; return nil }); err != nil {
		return "moved to " + dest + ", but saving it failed: " + err.Error()
	}
	_ = a.target(m.Target("")).CloseShared(ctx)
	return "moved from " + m.Host + "; address updated"
}

// sshUser is the user part of an ssh destination ("" when none).
func sshUser(host string) string {
	host = strings.TrimPrefix(host, "ssh://")
	if user, _, ok := strings.Cut(host, "@"); ok {
		return user
	}
	return ""
}

func newMachineDiscoverCmd(a *app) *cobra.Command {
	var f browseFlags
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Find Hive runtimes on the local network",
		Long: `Ask the local network (multicast DNS) for Hive runtimes that announce
themselves (discovery.advertise = true in their config), and list them with
their identity and address, and whether they are saved here. Nothing is
connected to or trusted; pair with one with ` + "`hive machine pair <name>`" + `.

A paired machine seen at a new address is followed there once it proves its
identity at it. Where multicast does not reach (other subnets, guest Wi-Fi,
some VPNs), --address asks a machine directly. When nothing is found, the
interfaces asked and what may be in the way are printed.`,
		Example: `  hive machine discover
  hive machine discover --interface en0 --timeout 5s
  hive machine discover --address 10.8.0.12`,
		Args: noArgs,
		RunE: withTimeout(machineTimeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			res, err := a.browse(ctx, f)
			if err != nil {
				return err
			}
			found := a.nearby(ctx, res)
			v := map[string]any{
				"runtimes": found, "interfaces": res.Interfaces, "skipped_interfaces": res.Skipped,
				"asked": nonNilStrings(res.Asked), "errors": nonNilStrings(res.Errors), "answers": res.Answers,
			}
			return a.emit(v, func() error {
				if len(found) == 0 {
					fmt.Fprintln(a.out, "no Hive runtimes found")
					a.explainBrowse(res)
					return nil
				}
				w := a.table()
				fmt.Fprintln(w, "NAME\tID\tADDRESS\tSTATE")
				for _, n := range found {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", n.Name, discovery.ShortID(n.ID), addrList(n.Record), describeNearby(n))
				}
				if err := w.Flush(); err != nil {
					return err
				}
				for _, n := range found {
					if n.Note != "" {
						fmt.Fprintf(a.errOut, "hive: %s: %s\n", cmpStr(n.Machine, n.Name), n.Note)
					}
				}
				return nil
			})
		}),
	}
	f.add(cmd)
	return cmd
}

func addrList(r discovery.Record) string {
	if len(r.Addrs) == 0 {
		return "-"
	}
	s := r.Addrs[0].String()
	if r.Port != 0 && r.Port != 22 {
		s += fmt.Sprintf(" (ssh %d)", r.Port)
	}
	if len(r.Addrs) > 1 {
		s += fmt.Sprintf(" +%d", len(r.Addrs)-1)
	}
	return s
}

func describeNearby(n nearbyRuntime) string {
	s := n.State
	switch n.State {
	case remote.StateDiscovered:
		s = "new: hive machine pair " + pairArg(n.Record)
	case remote.StateTrusted, remote.StateConfigured, remote.StateDisabled, remote.StateChanged:
		s += fmt.Sprintf(" (saved as %q)", n.Machine)
	}
	if n.DuplicateName {
		s += "; another runtime has this name"
	}
	return s
}

// pairArg is how to name r to pair: its name, or its ID when another
// runtime shares the name or it needs quoting.
func pairArg(r discovery.Record) string {
	if r.DuplicateName || strings.ContainsAny(r.Name, " ()") {
		return r.ID
	}
	return r.Name
}

// explainBrowse says, on stderr, where a browse that found nothing looked
// and what may be in the way.
func (a *app) explainBrowse(res discovery.Result) {
	e := a.errOut
	if len(res.Interfaces) > 0 {
		var names []string
		for _, i := range res.Interfaces {
			names = append(names, i.Name)
		}
		fmt.Fprintf(e, "hive: asked on %s\n", strings.Join(names, ", "))
	}
	if len(res.Asked) > 0 {
		fmt.Fprintf(e, "hive: asked %s directly\n", strings.Join(res.Asked, ", "))
	}
	for _, s := range res.Skipped {
		fmt.Fprintf(e, "hive: skipped %s: %s\n", s.Name, s.Skipped)
	}
	for _, err := range res.Errors {
		fmt.Fprintf(e, "hive: could not ask %s\n", err)
	}
	if res.Invalid > 0 {
		fmt.Fprintf(e, "hive: %d answers were not from a Hive runtime this version understands\n", res.Invalid)
	}
	fmt.Fprint(e, `hive: runtimes announce themselves only with discovery.advertise = true in
      their config, and while their daemon runs. Multicast (UDP port 5353) must
      reach them: the same network segment, firewalls that allow mDNS, and no
      client isolation (guest Wi-Fi). Without multicast, ask a machine directly
      (--address <ip>), or pair over ssh (hive machine pair --host user@host).
`)
}

// pickRuntime finds the runtime named arg (its name, or the start of its ID).
func pickRuntime(found []discovery.Record, arg string) (discovery.Record, error) {
	var match []discovery.Record
	for _, r := range found {
		if strings.EqualFold(r.Name, arg) || (len(arg) >= 4 && strings.HasPrefix(r.ID, strings.ToLower(arg))) {
			match = append(match, r)
		}
	}
	switch len(match) {
	case 1:
		return match[0], nil
	case 0:
		return discovery.Record{}, fmt.Errorf("no runtime called %q answered (`hive machine discover` lists those nearby)", arg)
	}
	var ids []string
	for _, r := range match {
		ids = append(ids, r.Name+" "+r.ID)
	}
	return discovery.Record{}, fmt.Errorf("%q names %d runtimes; name one by its ID: %s", arg, len(match), strings.Join(ids, ", "))
}

// localName turns a discovered name or host into a machine name.
func localName(s string) string {
	s = strings.TrimPrefix(s, "ssh://")
	if _, after, ok := strings.Cut(s, "@"); ok {
		s = after
	}
	if h, _, ok := strings.Cut(strings.TrimPrefix(s, "["), "]"); ok {
		s = h
	} else if h, _, ok := strings.Cut(s, ":"); ok && strings.Count(s, ":") == 1 {
		s = h
	}
	var b strings.Builder
	for _, r := range s {
		if r < 128 && (r == '.' || r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-._")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func newMachinePairCmd(a *app) *cobra.Command {
	var (
		f        browseFlags
		as, user string
		host     string
		m        remote.Machine
	)
	cmd := &cobra.Command{
		Use:   "pair <name|id>",
		Short: "Pair with a runtime found nearby: check its identity, then save it",
		Long: `Pair with a runtime that ` + "`hive machine discover`" + ` lists, named by its name or
the start of its ID. Hive connects over ssh (your keys and ~/.ssh/config apply),
asks the runtime to prove its identity, checks that it is the one announced,
and shows its identity, address and platform. It is saved, with its identity
pinned, only once you confirm (--yes confirms without asking).

An announcement is never trusted by itself: a runtime that cannot prove the
identity it announced is not paired. When the machine later proves another
identity, it is not connected to until ` + "`hive machine trust`" + ` accepts it.

With --host, pairing skips discovery: any ssh destination is paired the same
way, which also pins the identity of a machine on a network without multicast.`,
		Example: `  hive machine pair studio
  hive machine pair q3xdk2ab --as studio --user me
  hive machine pair --host me@10.8.0.12 --as vpn-box`,
		Args: func(cmd *cobra.Command, args []string) error {
			if host == "" {
				return exactArgs(1)(cmd, args)
			}
			return noArgs(cmd, args)
		},
		RunE: withTimeout(machineTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			var rec *discovery.Record
			m.Host = host
			if host == "" {
				res, err := a.browse(ctx, f)
				if err != nil {
					return err
				}
				r, err := pickRuntime(res.Runtimes, args[0])
				if err != nil {
					return err
				}
				if !slices.Contains(r.Caps, discovery.CapSSH) {
					return fmt.Errorf("%s does not offer ssh, which pairing needs", r.Name)
				}
				dest, ok := r.SSHDestination(user)
				if !ok {
					return fmt.Errorf("%s announced no address", r.Name)
				}
				rec, m.Host = &r, dest
				m.Discovered, m.HostKeyAlias = true, remote.HostKeyAlias(r.ID)
			}
			m.Name = as
			if m.Name == "" {
				m.Name = localName(m.Host)
				if rec != nil {
					m.Name = localName(rec.Name)
				}
			}
			if err := remote.ValidName(m.Name); err != nil {
				return &usageError{fmt.Errorf("%w; choose one with --as", err)}
			}
			if _, err := a.machines().Get(m.Name); err == nil {
				return fmt.Errorf("%w: %q (choose another name with --as)", remote.ErrMachineExists, m.Name)
			}
			if rec != nil {
				if old, ok, _ := a.machines().FindID(rec.ID); ok {
					return fmt.Errorf("%w: it is saved as %q", remote.ErrAlreadyPaired, old.Name)
				}
			}

			t := a.target(m.Target(""))
			b := *a
			b.remote, b.remoteName, b.client = &t, m.Name, a.remoteClient(t, "cli")
			defer b.client.Close()
			if err := b.ensureRemote(ctx); err != nil {
				return err
			}
			p, err := t.Identify(ctx)
			if err != nil {
				return fmt.Errorf("not paired: %w", err)
			}
			if rec != nil && p.ID != rec.ID {
				return fmt.Errorf("not paired: %s announced identity %s, but %s answered as %s; the announcement may not be its own",
					rec.Name, discovery.ShortID(rec.ID), m.Host, discovery.ShortID(p.ID))
			}
			if old, ok, _ := a.machines().FindID(p.ID); ok {
				return fmt.Errorf("%w: it is saved as %q", remote.ErrAlreadyPaired, old.Name)
			}
			a.showProof(m, p, "new: not paired before")
			if !a.confirm(fmt.Sprintf("trust this machine and save it as %q?", m.Name)) {
				return errors.New("not paired; run again with --yes to pair without asking")
			}
			m.Trust(p.ID, p.PublicKey, p.Platform)
			saved, err := a.machines().Add(m)
			if err != nil {
				return err
			}
			return a.done(map[string]any{"machine": saved}, "paired with %s (%s); use it with --machine %s",
				saved.Name, discovery.ShortID(saved.ID), saved.Name)
		}),
	}
	f.add(cmd)
	cmd.Flags().StringVar(&as, "as", "", "the name to save it under (default: its discovery name)")
	cmd.Flags().StringVar(&user, "user", "", "the ssh user there (default: ssh's)")
	cmd.Flags().StringVar(&host, "host", "", "pair this ssh destination instead of a discovered runtime")
	cmd.Flags().StringVar(&m.Session, "session", "", "the session to use there (default: its default session)")
	cmd.Flags().StringVar(&m.HivePath, "hive-path", "", "where hive is there (default: on its PATH, else ~/.local/bin/hive)")
	return cmd
}

// showProof prints, on stderr, who a machine proved to be.
func (a *app) showProof(m remote.Machine, p discovery.Proof, trust string) {
	e := a.errOut
	fmt.Fprintf(e, "hive: machine %q\n", m.Name)
	fmt.Fprintf(e, "  address:     %s\n", m.Host)
	fmt.Fprintf(e, "  identity:    %s\n", p.ID)
	fmt.Fprintf(e, "  fingerprint: %s\n", discovery.Fingerprint(p.Key()))
	fmt.Fprintf(e, "  name:        %s (host %s)\n", p.Name, cmpStr(p.Hostname, "?"))
	fmt.Fprintf(e, "  platform:    %s, hive %s\n", p.Platform, p.Version)
	fmt.Fprintf(e, "  trust:       %s\n", trust)
	fmt.Fprintln(e, "  (`hive machine identity` there shows its fingerprint, to compare)")
}

func newMachineTrustCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "trust <name>",
		Short: "Check a saved machine's identity, and pin it (or accept a changed one)",
		Long: `Connect to a saved machine and have it prove its identity. A machine saved
without one (` + "`hive machine add`" + `) gets it pinned. A machine whose identity
changed (hive reinstalled there, or its HIVE_HOME removed, or another machine at
its address) shows the old and the new, and is connected to again once you
accept the new one. --yes accepts without asking.`,
		Args: exactArgs(1),
		RunE: withTimeout(machineTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			m, err := a.machines().Get(args[0])
			if err != nil {
				return err
			}
			p, err := a.target(m.Target("")).Identify(ctx)
			if err != nil {
				return err
			}
			switch {
			case p.ID == m.ID:
				saved, err := a.machines().Update(m.Name, func(x *remote.Machine) error { x.IdentityChanged = false; return nil })
				if err != nil {
					return err
				}
				return a.done(map[string]any{"machine": saved, "changed": false}, "machine %q proved its pinned identity (%s)", m.Name, discovery.ShortID(m.ID))
			case m.ID == "":
				a.showProof(m, p, "saved without an identity; it is pinned now")
			default:
				a.showProof(m, p, fmt.Sprintf("CHANGED: it was paired as %s (%s)", m.ID, fingerprintOf(m.PublicKey)))
				fmt.Fprintln(a.errOut, "hive: if hive was not reinstalled there, and its HIVE_HOME not removed, another machine may be answering at its address")
			}
			if !a.confirm(fmt.Sprintf("trust machine %q as %s?", m.Name, discovery.ShortID(p.ID))) {
				return errors.New("not trusted; run again with --yes to trust it without asking")
			}
			saved, err := a.machines().Update(m.Name, func(x *remote.Machine) error {
				x.Trust(p.ID, p.PublicKey, p.Platform)
				return nil
			})
			if err != nil {
				return err
			}
			note := ""
			if saved.Disabled {
				note = fmt.Sprintf(" (it is disabled: `hive machine enable %s`)", saved.Name)
			}
			return a.done(map[string]any{"machine": saved, "changed": true}, "trusted machine %q as %s%s", saved.Name, discovery.ShortID(saved.ID), note)
		}),
	}
}

func fingerprintOf(publicKey string) string {
	p := discovery.Proof{PublicKey: publicKey}
	if k := p.Key(); len(k) > 0 {
		return discovery.Fingerprint(k)
	}
	return "no key"
}

func newMachineRevokeCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <name>",
		Short: "Stop trusting a paired machine: forget its identity and disable it",
		Long: `Forget the identity pinned for a machine and disable it, so nothing connects to
it until it is trusted again (` + "`hive machine trust`" + `, then ` + "`hive machine enable`" + `).
` + "`hive machine remove`" + ` forgets it altogether.`,
		Args: exactArgs(1),
		RunE: withTimeout(30*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			m, err := a.machines().Update(args[0], func(x *remote.Machine) error {
				x.ID, x.PublicKey, x.Platform, x.TrustedAt = "", "", "", time.Time{}
				x.IdentityChanged, x.Disabled = false, true
				return nil
			})
			if err != nil {
				return err
			}
			_ = a.target(m.Target("")).CloseShared(ctx)
			return a.done(map[string]any{"machine": m}, "revoked machine %q: its identity is forgotten and it is disabled", m.Name)
		}),
	}
}

// machineEntry is a row of `hive machine list`: a saved machine, or with
// --discover a runtime nearby that is not saved.
type machineEntry struct {
	remote.Machine
	State string `json:"state"`
	// Nearby says discovery found it.
	Nearby bool `json:"nearby,omitempty"`
	// Announcement is what an unsaved runtime announced.
	Announcement *discovery.Record `json:"announcement,omitempty"`
}

func machineEntries(saved []remote.Machine, nearby []nearbyRuntime) []machineEntry {
	out := make([]machineEntry, 0, len(saved)+len(nearby))
	for _, m := range saved {
		e := machineEntry{Machine: m, State: m.State()}
		e.Nearby = m.ID != "" && slices.ContainsFunc(nearby, func(n nearbyRuntime) bool { return n.ID == m.ID })
		out = append(out, e)
	}
	for _, n := range nearby {
		if n.Machine != "" {
			continue
		}
		rec := n.Record
		dest, _ := rec.SSHDestination("")
		out = append(out, machineEntry{Machine: remote.Machine{Name: rec.Name, Host: dest}, State: remote.StateDiscovered, Nearby: true, Announcement: &rec})
	}
	return out
}
