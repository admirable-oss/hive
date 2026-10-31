package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/access"
	"github.com/admirable-oss/hive/internal/remote"
	"github.com/admirable-oss/hive/internal/runtime"
	"github.com/admirable-oss/hive/internal/session"
)

func newAccessCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "access",
		Short: "Let teammates into this runtime over ssh, with a role",
		Long: `A member is a person you let into this runtime. They connect over ssh with
their own key, which Hive adds to your authorized_keys restricted to the
daemon's member socket: whatever they ask ssh to run, the key reaches only
` + "`hive proxy --member`" + `, which tells the daemon who they are. They use Hive as
usual (` + "`hive --remote you@host`" + `, ` + "`hive machine add`" + `).

Each member has a role per environment (observer, operator or owner) and an
expiry. Revoking a member ends their connections at once and removes their
key. Every connection, refusal and change is audited.

Managing members needs control of the daemon: your own socket, or --remote
as yourself. See docs/teams.md and RFC 0007.`,
	}
	cmd.AddCommand(
		newAccessInviteCmd(a),
		newAccessListCmd(a),
		newAccessShowCmd(a),
		newAccessGrantCmd(a),
		newAccessRenewCmd(a),
		newAccessRevokeCmd(a),
		newAccessAuditCmd(a),
		newAccessDoctorCmd(a),
		newAccessWhoamiCmd(a),
	)
	return cmd
}

// grantFlags are a role and where it applies.
type grantFlags struct {
	role string
	envs []string
	all  bool
}

func (f *grantFlags) add(cmd *cobra.Command, roleHelp string) {
	cmd.Flags().StringVar(&f.role, "role", "", roleHelp)
	cmd.Flags().StringArrayVar(&f.envs, "env", nil, "an environment the role applies in (repeatable)")
	cmd.Flags().BoolVar(&f.all, "all", false, "the role applies in every environment, those made later included")
}

func (f *grantFlags) grants() ([]access.Grant, error) {
	role, err := access.ParseRole(f.role)
	switch {
	case f.role == "":
		return nil, &usageError{errors.New("give --role: observer, operator or owner")}
	case err != nil:
		return nil, &usageError{err}
	case f.all == (len(f.envs) > 0):
		return nil, &usageError{errors.New("give --all, or --env for each environment (not both)")}
	}
	if f.all {
		return []access.Grant{{Environment: access.AllEnvironments, Role: role}}, nil
	}
	out := make([]access.Grant, len(f.envs))
	for i, e := range f.envs {
		out[i] = access.Grant{Environment: e, Role: role}
	}
	return out, nil
}

// readKey reads a public key from a file, or standard input for "-".
func readKey(path string) (access.PublicKey, string, error) {
	var (
		b   []byte
		err error
	)
	if path == "-" {
		b, err = io.ReadAll(io.LimitReader(os.Stdin, 16<<10))
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return access.PublicKey{}, "", err
	}
	text := strings.TrimSpace(string(b))
	if strings.Contains(text, "\n") {
		return access.PublicKey{}, "", &usageError{errors.New("the key file holds more than one line: give one public key")}
	}
	k, err := access.ParseKey(text)
	if err != nil {
		return access.PublicKey{}, "", &usageError{err}
	}
	return k, text, nil
}

// where names the machine commands act on, for questions.
func (a *app) where() string {
	if a.remote != nil {
		return a.remoteName
	}
	return "this machine"
}

func newAccessInviteCmd(a *app) *cobra.Command {
	var (
		g       grantFlags
		keyPath string
		expires time.Duration
		printIt bool
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "invite <name>",
		Short: "Let a teammate in with their ssh key and a role",
		Long: `Make a member and add their key to your authorized_keys, inside a block Hive
manages, restricted to the member socket:

  command="hive proxy --member m_…",restrict ssh-ed25519 AAAA… hive:alice

It shows what it will add and asks first (--yes answers). With --print it
changes nothing there and prints the line, for keys installed by
configuration management.`,
		Example: `  hive access invite alice --key alice.pub --role operator --env web
  hive access invite bob --key bob.pub --role observer --all --expires 720h
  hive access invite ci --key ci.pub --role observer --all --print`,
		Args: exactArgs(1),
		RunE: withTimeout(30*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			if keyPath == "" {
				return &usageError{errors.New("give --key: the member's public key file (as in ~/.ssh/id_ed25519.pub; - reads standard input)")}
			}
			grants, err := g.grants()
			if err != nil {
				return err
			}
			key, text, err := readKey(keyPath)
			if err != nil {
				return err
			}
			if !printIt && !a.confirm(fmt.Sprintf("add %s's key (%s) to authorized_keys on %s, restricted to Hive's member socket?", args[0], key.Fingerprint(), a.where())) {
				return &usageError{errors.New("not invited: answer yes, pass --yes, or --print to install the line yourself")}
			}
			var res runtime.AccessInvited
			if err := a.client.Call(ctx, "access.invite", runtime.AccessInviteParams{
				Name: args[0], Key: text, Grants: grants, TTLSeconds: int64(expires / time.Second), Print: printIt,
			}, &res); err != nil {
				return err
			}
			return a.emit(res, func() error {
				m := res.Member
				fmt.Fprintf(a.out, "Invited %s (%s)\n", m.Name, m.ID)
				fmt.Fprintf(a.out, "  roles:    %s\n  key:      %s\n  expires:  %s\n", m.GrantsString(), m.Fingerprint, m.ExpiresAt.Local().Format(time.DateTime))
				if res.AuthorizedKeys != "" {
					fmt.Fprintf(a.out, "  added to: %s\n\n", res.AuthorizedKeys)
				} else {
					fmt.Fprintf(a.out, "\nAdd this line to the authorized_keys of the account running Hive on %s:\n%s\n\n", a.where(), res.Line)
				}
				fmt.Fprintf(a.out, "%s connects over ssh as this account, with their own key:\n`hive --remote <user>@<host>`, or a saved machine (`hive machine add`).\n", m.Name)
				return nil
			})
		}),
	})
	g.add(cmd, "their role: observer (reads), operator (prompts agents and approves tasks, starts nothing) or owner")
	cmd.Flags().StringVar(&keyPath, "key", "", "the member's ssh public key `file` (- for standard input)")
	cmd.Flags().DurationVar(&expires, "expires", access.DefaultTTL, "how long their access lasts (1h to 8760h)")
	cmd.Flags().BoolVar(&printIt, "print", false, "leave authorized_keys alone and print the line to install")
	return cmd
}

func newAccessListCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List members, their roles, expiry and open connections",
		Args:    noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			var res runtime.AccessList
			if err := a.client.Call(ctx, "access.list", nil, &res); err != nil {
				return err
			}
			return a.emit(res, func() error {
				if !res.Enabled {
					fmt.Fprintln(a.errOut, "hive: the member socket is off (access.enabled = false)")
				}
				if len(res.Members) == 0 {
					fmt.Fprintln(a.out, "no members (hive access invite)")
					return nil
				}
				w := a.table()
				fmt.Fprintln(w, "ID\tNAME\tSTATE\tROLES\tEXPIRES\tLAST SEEN\tCONNECTIONS")
				for _, m := range res.Members {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\n", m.ID, m.Name, m.State, m.GrantsString(),
						m.ExpiresAt.Local().Format(time.DateTime), lastUsed(m.LastSeenAt), m.Connections)
				}
				return w.Flush()
			})
		}),
	})
}

func printMember(w io.Writer, m access.Member) {
	fmt.Fprintf(w, "id:          %s\nname:        %s\nstate:       %s\nroles:       %s\nkey:         %s\n", m.ID, m.Name, m.State, m.GrantsString(), m.Fingerprint)
	managed := "yes"
	if !m.Managed {
		managed = "no (installed by hand)"
	}
	fmt.Fprintf(w, "managed:     %s\ncreated:     %s\nexpires:     %s\nlast seen:   %s\n", managed,
		m.CreatedAt.Local().Format(time.DateTime), m.ExpiresAt.Local().Format(time.DateTime), lastUsed(m.LastSeenAt))
	if m.RevokedAt != nil {
		fmt.Fprintf(w, "revoked:     %s\n", m.RevokedAt.Local().Format(time.DateTime))
	}
}

func newAccessShowCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "show <member>",
		Short: "Show a member, by name or ID",
		Args:  exactArgs(1),
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			var m runtime.AccessMember
			if err := a.client.Call(ctx, "access.get", map[string]string{"member": args[0]}, &m); err != nil {
				return err
			}
			return a.emit(m, func() error {
				printMember(a.out, m.Member)
				fmt.Fprintf(a.out, "connections: %d\n", m.Connections)
				return nil
			})
		}),
	})
}

func newAccessGrantCmd(a *app) *cobra.Command {
	var g grantFlags
	cmd := needsDaemon(&cobra.Command{
		Use:   "grant <member>",
		Short: "Change a member's role in environments (none takes it away)",
		Example: `  hive access grant alice --env api --role observer
  hive access grant alice --all --role none`,
		Args: exactArgs(1),
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			grants, err := g.grants()
			if err != nil {
				return err
			}
			var m access.Member
			for _, gr := range grants {
				if err := a.client.Call(ctx, "access.grant", map[string]any{"member": args[0], "environment": gr.Environment, "role": gr.Role}, &m); err != nil {
					return err
				}
			}
			return a.done(map[string]any{"member": m}, "%s's roles: %s", m.Name, m.GrantsString())
		}),
	})
	g.add(cmd, "the role: observer, operator, owner, or none to take it away")
	return cmd
}

func newAccessRenewCmd(a *app) *cobra.Command {
	var expires time.Duration
	cmd := needsDaemon(&cobra.Command{
		Use:   "renew <member>",
		Short: "Extend a member's access, from now",
		Args:  exactArgs(1),
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			var m access.Member
			if err := a.client.Call(ctx, "access.renew", map[string]any{"member": args[0], "ttl_seconds": int64(expires / time.Second)}, &m); err != nil {
				return err
			}
			return a.done(map[string]any{"member": m}, "%s's access now expires %s", m.Name, m.ExpiresAt.Local().Format(time.DateTime))
		}),
	})
	cmd.Flags().DurationVar(&expires, "expires", access.DefaultTTL, "how long from now (1h to 8760h)")
	return cmd
}

func newAccessRevokeCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "revoke <member>",
		Short: "End a member's access: their connections end at once, their key is removed",
		Args:  exactArgs(1),
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			var m access.Member
			if err := a.client.Call(ctx, "access.revoke", map[string]string{"member": args[0]}, &m); err != nil {
				return err
			}
			return a.done(map[string]any{"member": m}, "revoked %s (%s)", m.Name, m.ID)
		}),
	})
}

func newAccessAuditCmd(a *app) *cobra.Command {
	var (
		member string
		limit  int
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "audit",
		Short: "Show who did what: invitations, changes, connections, refusals",
		Args:  noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			var entries []access.AuditEntry
			if err := a.client.Call(ctx, "access.audit", map[string]any{"member": member, "limit": limit}, &entries); err != nil {
				return err
			}
			return a.emit(entries, func() error {
				w := a.table()
				fmt.Fprintln(w, "TIME\tACTION\tMEMBER\tFROM\tCLIENT\tDETAIL")
				for _, e := range entries {
					detail := e.Detail
					if e.Method != "" {
						detail = strings.TrimSpace(e.Method + " " + detail)
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Time.Local().Format(time.DateTime), e.Action, dash(e.Name), dash(e.From), dash(e.Client), dash(detail))
				}
				return w.Flush()
			})
		}),
	})
	cmd.Flags().StringVar(&member, "member", "", "only this member's entries (name or ID)")
	cmd.Flags().IntVar(&limit, "limit", 100, "how many of the newest entries")
	return cmd
}

func newAccessDoctorCmd(a *app) *cobra.Command {
	var fix bool
	cmd := needsDaemon(&cobra.Command{
		Use:   "doctor",
		Short: "Check authorized_keys against the members",
		Long: `Report members whose key line is missing or changed, lines in Hive's block
that belong to no active member, and members whose key also logs in outside
the block, without restriction. --fix rewrites Hive's block; lines outside it
are never changed. Exits 1 when it finds a problem it did not fix.`,
		Args: noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			var res runtime.AccessDoctor
			if err := a.client.Call(ctx, "access.doctor", map[string]bool{"fix": fix}, &res); err != nil {
				return err
			}
			if err := a.emit(res, func() error {
				if len(res.Problems) == 0 {
					fmt.Fprintf(a.out, "%s matches the members\n", res.AuthorizedKeys)
					return nil
				}
				for _, p := range res.Problems {
					fmt.Fprintln(a.out, "- "+p)
				}
				if res.Fixed {
					fmt.Fprintf(a.out, "rewrote Hive's block in %s\n", res.AuthorizedKeys)
				}
				return nil
			}); err != nil {
				return err
			}
			if len(res.Problems) > 0 && !res.Fixed {
				return &codedError{code: exitError}
			}
			return nil
		}),
	})
	cmd.Flags().BoolVar(&fix, "fix", false, "rewrite Hive's block in authorized_keys to match the members")
	return cmd
}

func newAccessWhoamiCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "whoami",
		Short: "Show who this connection is to the daemon: its owner, or a member",
		Args:  noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			var w runtime.WhoAmI
			if err := a.client.Call(ctx, "access.whoami", nil, &w); err != nil {
				return err
			}
			return a.emit(w, func() error {
				if w.Member == nil {
					fmt.Fprintln(a.out, "owner (this runtime's own user)")
					return nil
				}
				printMember(a.out, *w.Member)
				if w.From != "" {
					fmt.Fprintf(a.out, "from:        %s\n", w.From)
				}
				return nil
			})
		}),
	})
}

// memberProxyCommand is what members' keys are forced to run, up to the
// member's ID: this hive, with the session's data, bridging to the member
// socket. It names hive by a path that survives upgrades when one finds
// the same binary (a package manager's link), since a login shell's PATH
// for a forced command is often short.
func (a *app) memberProxyCommand(exe string) string {
	bin := exe
	if p, err := exec.LookPath("hive"); err == nil && sameFile(p, exe) {
		if abs, err := filepath.Abs(p); err == nil {
			bin = abs
		}
	}
	var parts []string
	if a.getenv("HIVE_HOME") != "" {
		parts = append(parts, "env", "HIVE_HOME="+remote.Quote(a.base))
	}
	parts = append(parts, remote.Quote(bin))
	if a.session != session.Default {
		parts = append(parts, "--session", remote.Quote(a.session))
	}
	return strings.Join(append(parts, "proxy", "--member"), " ")
}

func sameFile(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
