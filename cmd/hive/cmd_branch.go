package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/branch"
	"github.com/admirable-oss/hive/internal/client"
)

// branchTimeout bounds branch commands that only read or record.
const branchTimeout = 2 * time.Minute

// flusher is a writer with a buffer, like a.table().
type flusher interface {
	io.Writer
	Flush() error
}

func newBranchCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "branch",
		Aliases: []string{"branches", "br"},
		Short:   "Follow agents' branches: their state, overlaps, review and publishing",
		Long: `Hive follows the branch of every worktree it makes (for an agent, a task, or
` + "`hive worktree create`" + `) from its creation to its cleanup:

  created   the worktree exists; nothing happened in it yet
  working   an agent worked there, or it differs from its base
  ready     its gates (tests, lint, build) passed on the files as they are
  merged    its commits are in its base
  cleaned   merged, and its worktree removed
  removed   its worktree was removed before it merged (the branch is kept)

Branches that change the same files are reported as they do, with the files
a merge would conflict on, uncommitted work included, before anyone merges.

Review a branch with ` + "`diff`" + `, send it ` + "`comment`" + `s that go back to its agent
as a prompt, and run its gates with ` + "`ready`" + `. Hive never commits, merges,
resolves a conflict or pushes by itself: ` + "`update`" + `, ` + "`push`" + ` and ` + "`pr`" + ` run only
when you ask. A merged branch's worktree is removed once nothing
uncommitted and no running process is in it ([branches] cleanup).

A branch is named by its ID (its environment's), an agent's ID, plan/task,
or its name.`,
		Example: `  hive agent start --kind claude --env api --isolate
  hive branch list
  hive branch diff hive/claude/3f2a91
  hive branch comment p12 "Handle the error" --file api/user.go --line 42 --send
  hive branch ready p12
  hive branch pr p12 --push`,
	}
	cmd.AddCommand(newBranchListCmd(a), newBranchGetCmd(a), newBranchCheckCmd(a), newBranchDiffCmd(a),
		newBranchConflictsCmd(a), newBranchUpdateCmd(a), newBranchReadyCmd(a), newBranchCommentCmd(a),
		newBranchSendCmd(a), newBranchPushCmd(a), newBranchPRCmd(a), newBranchCleanCmd(a))
	return cmd
}

func newBranchListCmd(a *app) *cobra.Command {
	var (
		repo string
		all  bool
	)
	cmd := needsDaemon(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the branches Hive follows",
		Args:    noArgs,
		RunE: withTimeout(branchTimeout, func(ctx context.Context, cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("repo") {
				dir, err := a.dirArg(repo)
				if err != nil {
					return err
				}
				repo = dir
			}
			list, err := client.NewBranches(a.client).List(ctx, repo, all)
			if err != nil {
				return err
			}
			return a.emit(list, func() error { return writeBranchTable(a.table(), list) })
		}),
	})
	cmd.Flags().StringVar(&repo, "repo", "", "only this repository's branches (any directory of it)")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include cleaned and removed branches")
	return cmd
}

func writeBranchTable(w flusher, list []branch.Branch) error {
	if len(list) == 0 {
		fmt.Fprintln(w, "no branches (agents started with --isolate or --worktree get one)")
		return w.Flush()
	}
	fmt.Fprintln(w, "ID\tBRANCH\tSTATE\tBASE\tCHANGES\tOVERLAPS\tPULL REQUEST")
	for _, b := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", b.ID, b.Name, branchState(b), b.Base, branchChanges(b), branchOverlaps(b), cmpStr(prOf(b), "-"))
	}
	return w.Flush()
}

// branchState is a branch's state with what qualifies it.
func branchState(b branch.Branch) string {
	s := string(b.State)
	switch {
	case b.Checking:
		s += " (checking)"
	case b.InProgress:
		s += " (merge stopped)"
	case b.Error != "":
		s += " (unreadable)"
	}
	return s
}

func branchChanges(b branch.Branch) string {
	if b.Files == 0 && b.Ahead == 0 {
		return "-"
	}
	s := fmt.Sprintf("%d files +%d -%d", b.Files, b.Added, b.Deleted)
	if b.Dirty {
		s += " *"
	}
	return s
}

func branchOverlaps(b branch.Branch) string {
	if len(b.Overlaps) == 0 {
		return "-"
	}
	var parts []string
	for _, o := range b.Overlaps {
		p := fmt.Sprintf("%s (%d)", o.Branch, len(o.Files))
		if len(o.Conflicts) > 0 {
			p = fmt.Sprintf("%s (%d conflicting)", o.Branch, len(o.Conflicts))
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ", ")
}

func prOf(b branch.Branch) string {
	if b.PullRequest == nil {
		return ""
	}
	return b.PullRequest.URL
}

func newBranchGetCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "get <branch>",
		Short: "Show a branch: its state, changes, overlaps, gates and comments",
		Args:  exactArgs(1),
		RunE: withTimeout(branchTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			b, err := client.NewBranches(a.client).Get(ctx, args[0])
			if err != nil {
				return err
			}
			return a.emit(b, func() error { return writeBranch(a.out, b) })
		}),
	})
}

// writeBranch prints a branch for people.
func writeBranch(out io.Writer, b branch.Branch) error {
	w := &errWriter{w: out}
	w.printf("%s  %s  (id %s)\n", b.Name, branchState(b), b.ID)
	w.printf("  repository  %s\n", b.Repo)
	base := b.Base
	if b.BaseCommit != "" && b.BaseCommit != b.Base {
		base += " (from " + shortHash(b.BaseCommit) + ")"
	}
	if b.Ahead+b.Behind > 0 {
		base += fmt.Sprintf(", %d ahead, %d behind", b.Ahead, b.Behind)
	}
	w.printf("  base        %s\n", base)
	if b.Path != "" {
		wt := b.Path
		if b.Dirty {
			wt += " (uncommitted changes)"
		}
		w.printf("  worktree    %s\n", wt)
	}
	if o := b.Owner; o != nil {
		var who []string
		if o.Kind != "" || o.Agent != "" {
			who = append(who, strings.TrimSpace(o.Kind+" agent "+o.Agent))
		}
		if o.Plan != "" {
			who = append(who, "task "+o.Plan+"/"+o.Task)
		}
		w.printf("  for         %s\n", strings.Join(who, ", "))
	}
	if b.Files > 0 {
		w.printf("  changes     %d files, +%d -%d\n", b.Files, b.Added, b.Deleted)
	}
	for i, o := range b.Overlaps {
		label := "overlaps"
		if i > 0 {
			label = ""
		}
		line := fmt.Sprintf("%s: %s", o.Branch, strings.Join(o.Files, ", "))
		switch {
		case len(o.Conflicts) > 0:
			line += " (conflicts in " + strings.Join(o.Conflicts, ", ") + ")"
		case o.Unknown:
			line += " (conflicts unknown: git 2.38 or newer finds them)"
		}
		w.printf("  %-10s  %s\n", label, line)
	}
	if b.InProgress {
		w.printf("  merge       stopped on a conflict: resolve it and commit, or hive branch update %s --abort\n", b.ID)
	}
	if len(b.Gates) > 0 {
		var gates []string
		for _, g := range b.Gates {
			gates = append(gates, gateSummary(g))
		}
		w.printf("  gates       %s\n", strings.Join(gates, "; "))
	}
	if b.ReadyAt != nil && b.State == branch.StateReady {
		w.printf("  ready       since %s\n", b.ReadyAt.Local().Format("Jan 2 15:04"))
	}
	if n := len(b.Comments); n > 0 {
		w.printf("  comments    %d (%d not sent)\n", n, b.Pending())
	}
	if b.Pushed != nil {
		w.printf("  pushed      %s to %s, %s\n", shortHash(b.Pushed.Commit), b.Pushed.Remote, b.Pushed.At.Local().Format("Jan 2 15:04"))
	}
	if b.PullRequest != nil {
		w.printf("  pull req.   %s\n", b.PullRequest.URL)
	}
	if b.Cleanup != "" {
		w.printf("  kept        %s\n", b.Cleanup)
	}
	if b.Saved != "" {
		w.printf("  saved       uncommitted work in %s\n", b.Saved)
	}
	if b.Error != "" {
		w.printf("  error       %s\n", b.Error)
	}
	return w.err
}

func gateSummary(g branch.GateResult) string {
	switch {
	case g.Skipped:
		return g.Name + ": skipped"
	case g.Passed:
		return fmt.Sprintf("%s: ok (%s)", g.Name, (time.Duration(g.DurationMS) * time.Millisecond).Round(100*time.Millisecond))
	case g.Error != "":
		return g.Name + ": " + g.Error
	}
	return fmt.Sprintf("%s: failed (exit %d)", g.Name, g.ExitCode)
}

// errWriter keeps the first write error.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err == nil {
		_, e.err = fmt.Fprintf(e.w, format, args...)
	}
}

func newBranchCheckCmd(a *app) *cobra.Command {
	var repo string
	cmd := needsDaemon(&cobra.Command{
		Use:   "check [branch]",
		Short: "Read branches again now: states, changes and overlaps",
		Long: `Read branches again now, rather than at the next change or interval: a
branch's repository, --repo's, or every repository with branches.`,
		Args: rangeArgs(0, 1),
		RunE: withTimeout(branchTimeout, func(ctx context.Context, cmd *cobra.Command, args []string) error {
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			if cmd.Flags().Changed("repo") {
				dir, err := a.dirArg(repo)
				if err != nil {
					return err
				}
				repo = dir
			}
			list, err := client.NewBranches(a.client).Check(ctx, ref, repo)
			if err != nil {
				return err
			}
			return a.emit(list, func() error { return writeBranchTable(a.table(), list) })
		}),
	})
	cmd.Flags().StringVar(&repo, "repo", "", "this repository (any directory of it)")
	return cmd
}

func newBranchDiffCmd(a *app) *cobra.Command {
	var (
		req   branch.DiffRequest
		lines int
		color string
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "diff <branch> [path...]",
		Short: "Show a branch's changes against its base, uncommitted ones included",
		Long: `Show what a branch changes since it forked from its base: its commits, its
uncommitted changes and its new files, as one unified diff. Its base's own
changes since are not shown.`,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) < 1 {
				return &usageError{errors.New("name a branch")}
			}
			return nil
		},
		RunE: withTimeout(branchTimeout, func(ctx context.Context, cmd *cobra.Command, args []string) error {
			req.Ref, req.Paths = args[0], args[1:]
			if cmd.Flags().Changed("unified") {
				req.Context = &lines
			}
			d, err := client.NewBranches(a.client).Diff(ctx, req)
			if err != nil {
				return err
			}
			return a.emit(d, func() error {
				if req.Stat {
					return writeDiffStat(a.table(), d)
				}
				if _, err := io.WriteString(a.out, colorDiff(d.Patch, useColor(a.out, color))); err != nil {
					return err
				}
				if d.Truncated {
					fmt.Fprintf(a.errOut, "(cut at %d bytes; name the files, or raise --limit)\n", len(d.Patch))
				}
				return nil
			})
		}),
	})
	cmd.Flags().BoolVar(&req.Stat, "stat", false, "only the files and their line counts")
	cmd.Flags().IntVarP(&lines, "unified", "U", 3, "lines of context")
	cmd.Flags().IntVar(&req.Limit, "limit", 0, "the most bytes of patch (default 512 KiB, at most 4 MiB)")
	cmd.Flags().StringVar(&color, "color", "auto", "color the diff: auto, always or never")
	return cmd
}

func writeDiffStat(w flusher, d branch.Diff) error {
	for _, f := range d.Files {
		counts := fmt.Sprintf("+%d -%d", f.Added, f.Deleted)
		if f.Added < 0 {
			counts = "binary"
		}
		path := f.Path
		if f.From != "" {
			path = f.From + " -> " + f.Path
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", f.Status, counts, path)
	}
	return w.Flush()
}

// useColor decides whether to color output for --color.
func useColor(out io.Writer, mode string) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := out.(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

// colorDiff colors a unified diff's lines as git does.
func colorDiff(patch string, color bool) string {
	if !color {
		return patch
	}
	var b strings.Builder
	for line := range strings.SplitAfterSeq(patch, "\n") {
		code := ""
		switch {
		case strings.HasPrefix(line, "diff --git"), strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "--- "):
			code = "1"
		case strings.HasPrefix(line, "@@"):
			code = "36"
		case strings.HasPrefix(line, "+"):
			code = "32"
		case strings.HasPrefix(line, "-"):
			code = "31"
		}
		if code == "" {
			b.WriteString(line)
			continue
		}
		body, nl := strings.CutSuffix(line, "\n")
		b.WriteString("\x1b[" + code + "m" + body + "\x1b[0m")
		if nl {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func newBranchConflictsCmd(a *app) *cobra.Command {
	var with string
	cmd := needsDaemon(&cobra.Command{
		Use:   "conflicts <branch>",
		Short: "Show what merging a branch would conflict on, without merging",
		Long: `Merge a branch, as it is now (uncommitted work included), with its base or
another branch (--with) in memory, and show each conflicted file with its
conflict markers. Nothing is merged and no worktree is touched. It exits 1
when there are conflicts.`,
		Args: exactArgs(1),
		RunE: withTimeout(branchTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			c, err := client.NewBranches(a.client).Conflicts(ctx, args[0], with)
			if err != nil {
				return err
			}
			if err := a.emit(c, func() error {
				if c.Clean {
					_, err := fmt.Fprintf(a.out, "%s merges with %s without conflicts\n", c.Branch, c.With)
					return err
				}
				for _, f := range c.Files {
					fmt.Fprintf(a.out, "==> %s <==\n%s", f.Path, f.Content)
					if !strings.HasSuffix(f.Content, "\n") {
						fmt.Fprintln(a.out)
					}
					if f.Truncated {
						fmt.Fprintln(a.out, "(cut)")
					}
				}
				return nil
			}); err != nil {
				return err
			}
			if !c.Clean {
				return &codedError{code: exitError, msg: fmt.Sprintf("%d files conflict between %s and %s", len(c.Files), c.Branch, c.With)}
			}
			return nil
		}),
	})
	cmd.Flags().StringVar(&with, "with", "", "another branch (default: its base)")
	return cmd
}

func newBranchUpdateCmd(a *app) *cobra.Command {
	var req branch.UpdateRequest
	cmd := needsDaemon(&cobra.Command{
		Use:   "update <branch>",
		Short: "Merge a branch's base into it (or rebase onto it), stopping on conflicts",
		Long: `Merge a branch's base into it in its worktree, or with --rebase rebase it
onto its base. This is your merge, made because you asked: it refuses while
an agent works there or files are uncommitted, and on a conflict it stops and
leaves the files for you (resolve them and commit, or --abort). Hive never
resolves a conflict.`,
		Args: exactArgs(1),
		RunE: withTimeout(10*time.Minute, func(ctx context.Context, _ *cobra.Command, args []string) error {
			req.Ref = args[0]
			res, err := client.NewBranches(a.client).Update(ctx, req)
			if err != nil {
				return err
			}
			if err := a.emit(res, func() error {
				switch {
				case req.Abort:
					fmt.Fprintf(a.out, "aborted; %s is as it was\n", res.Branch.Name)
				case len(res.Conflicts) > 0:
					fmt.Fprintf(a.out, "stopped on conflicts in %s (in %s):\n", res.Branch.Name, res.Branch.Path)
					for _, c := range res.Conflicts {
						fmt.Fprintf(a.out, "  %s\n", c)
					}
					fmt.Fprintf(a.out, "resolve them and commit, or: hive branch update %s --abort\n", res.Branch.ID)
				default:
					fmt.Fprintf(a.out, "%s is up to date with %s\n", res.Branch.Name, res.Branch.Base)
				}
				return nil
			}); err != nil {
				return err
			}
			if len(res.Conflicts) > 0 {
				return &codedError{code: exitError, msg: "conflicts left for you"}
			}
			return nil
		}),
	})
	cmd.Flags().BoolVar(&req.Rebase, "rebase", false, "rebase onto the base instead of merging it")
	cmd.Flags().BoolVar(&req.Abort, "abort", false, "undo a merge or rebase stopped on a conflict")
	return cmd
}

func newBranchReadyCmd(a *app) *cobra.Command {
	var name string
	cmd := needsDaemon(&cobra.Command{
		Use:   "ready <branch> [-- command args...]",
		Short: "Run a branch's gates; it is ready when they pass",
		Long: `Run a branch's gates ([[branches.gate]]: tests, lint, build) in its worktree,
in order, stopping at the first that fails. When they all pass on files that
did not change meanwhile, the branch is ready, until its files change again.
A command after -- runs instead of the configured gates. It exits 1 when a
gate fails.`,
		Example: `  hive branch ready p12
  hive branch ready hive/claude/3f2a91 -- go test ./...`,
		Args: argsBeforeDash(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			before, after := splitAtDash(cmd, args)
			var gates []branch.Gate
			if len(after) > 0 {
				gates = []branch.Gate{{Name: cmpStr(name, after[0]), Command: after}}
			}
			b, err := client.NewBranches(a.client).Ready(cmd.Context(), before[0], gates)
			if err != nil {
				return err
			}
			if err := a.emit(b, func() error {
				for _, g := range b.Gates {
					fmt.Fprintln(a.out, gateSummary(g))
					if !g.Passed && !g.Skipped {
						for _, l := range g.Output {
							fmt.Fprintf(a.out, "  %s\n", l)
						}
					}
				}
				if b.State == branch.StateReady {
					fmt.Fprintf(a.out, "%s is ready\n", b.Name)
				}
				return nil
			}); err != nil {
				return err
			}
			if b.State != branch.StateReady {
				return &codedError{code: exitError, msg: b.Name + " is not ready"}
			}
			return nil
		},
	})
	cmd.Flags().StringVar(&name, "name", "", "the name of the gate given after --")
	return cmd
}

func newBranchCommentCmd(a *app) *cobra.Command {
	var req branch.CommentRequest
	cmd := needsDaemon(&cobra.Command{
		Use:   "comment <branch> <text...>",
		Short: "Add a review comment, which goes back to the branch's agent",
		Long: `Add a review comment to a branch, about a file (--file) and a line (--line)
of its diff, or the whole change. Comments go to the branch's agent as one
prompt when you send them (` + "`hive branch send`" + `, or --send now).`,
		Example: `  hive branch comment p12 "Handle the error here" --file api/user.go --line 42
  hive branch comment p12 "Add a test for the empty case" --send`,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) < 2 {
				return &usageError{errors.New("name a branch and write the comment")}
			}
			return nil
		},
		RunE: withTimeout(time.Minute, func(ctx context.Context, _ *cobra.Command, args []string) error {
			r := req
			r.Ref, r.Body = args[0], strings.Join(args[1:], " ")
			if r.By == "" {
				r.By = a.getenv("USER")
			}
			b, err := client.NewBranches(a.client).Comment(ctx, r)
			if err != nil {
				return err
			}
			return a.emit(b, func() error {
				if r.Send {
					_, err := fmt.Fprintf(a.out, "sent to the agent on %s\n", b.Name)
					return err
				}
				_, err := fmt.Fprintf(a.out, "%d comments on %s (%d not sent: hive branch send %s)\n", len(b.Comments), b.Name, b.Pending(), b.ID)
				return err
			})
		}),
	})
	cmd.Flags().StringVar(&req.Path, "file", "", "the file it is about")
	cmd.Flags().IntVar(&req.Line, "line", 0, "the line it is about")
	cmd.Flags().BoolVar(&req.Send, "send", false, "send it, and every comment not sent yet, to the agent now")
	cmd.Flags().StringVar(&req.By, "by", "", "who wrote it (default $USER)")
	return cmd
}

func newBranchSendCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "send <branch>",
		Short: "Send a branch's comments to its agent, as one prompt",
		Args:  exactArgs(1),
		RunE: withTimeout(time.Minute, func(ctx context.Context, _ *cobra.Command, args []string) error {
			b, err := client.NewBranches(a.client).Send(ctx, args[0])
			if err != nil {
				return err
			}
			return a.done(map[string]any{"branch": b.ID}, "sent the comments on %s to its agent", b.Name)
		}),
	})
}

func newBranchPushCmd(a *app) *cobra.Command {
	var remote string
	cmd := needsDaemon(&cobra.Command{
		Use:   "push <branch>",
		Short: "Push a branch to its remote (never forced)",
		Long: `Push a branch's commits to the branch of the same name on a remote (its
upstream's, else origin), and make it the upstream. Hive pushes only when you
ask, and never forces. Uncommitted changes are not pushed.`,
		Args: exactArgs(1),
		RunE: withTimeout(5*time.Minute, func(ctx context.Context, _ *cobra.Command, args []string) error {
			b, err := client.NewBranches(a.client).Push(ctx, args[0], remote)
			if err != nil {
				return err
			}
			return a.emit(b, func() error {
				_, err := fmt.Fprintf(a.out, "pushed %s to %s (%s)\n", b.Name, b.Pushed.Remote, shortHash(b.Pushed.Commit))
				return err
			})
		}),
	})
	cmd.Flags().StringVar(&remote, "remote", "", "the remote (default: its upstream's, else origin)")
	return cmd
}

func newBranchPRCmd(a *app) *cobra.Command {
	var req branch.PullRequestRequest
	cmd := needsDaemon(&cobra.Command{
		Use:     "pr <branch>",
		Aliases: []string{"pull-request"},
		Short:   "Open a pull request for a branch (GitHub, GitLab, or a configured forge)",
		Long: `Open a pull request for a branch's commits, with gh (GitHub) or glab (GitLab)
by the remote's host, or a forge configured in [[branches.forge]]. It refuses
a branch with uncommitted changes (commit them first) or nothing to propose,
and one its remote does not have: push it first, or give --push.

The title defaults to its first commit's subject, and the description to its
commits and the gates it passed.`,
		Args: exactArgs(1),
		RunE: withTimeout(5*time.Minute, func(ctx context.Context, _ *cobra.Command, args []string) error {
			r := req
			r.Ref = args[0]
			b, err := client.NewBranches(a.client).PullRequest(ctx, r)
			if err != nil {
				return err
			}
			return a.emit(b, func() error {
				_, err := fmt.Fprintln(a.out, b.PullRequest.URL)
				return err
			})
		}),
	})
	f := cmd.Flags()
	f.StringVar(&req.Title, "title", "", "the title (default: its first commit's subject)")
	f.StringVar(&req.Body, "body", "", "the description (default: its commits and gates)")
	f.BoolVar(&req.Draft, "draft", false, "open it as a draft")
	f.StringVar(&req.Base, "base", "", "the branch to merge into (default: the branch's base)")
	f.BoolVar(&req.Push, "push", false, "push the branch first if its remote lacks its commits")
	f.StringVar(&req.Remote, "remote", "", "the remote (default: its upstream's, else origin)")
	return cmd
}

func newBranchCleanCmd(a *app) *cobra.Command {
	var req branch.CleanRequest
	cmd := needsDaemon(&cobra.Command{
		Use:   "clean [branch]",
		Short: "Remove a branch's worktree (or every merged one's)",
		Long: `Remove a branch's worktree and the environments rooted in it, stopping their
agents; --merged does it for every merged branch. The branch and its commits
stay in the repository. A worktree with uncommitted changes is kept unless
--force, which saves them under refs/hive/saved/ first, so nothing is lost.`,
		Args: rangeArgs(0, 1),
		RunE: withTimeout(worktreeTimeout+a.cfg.Daemon.ShutdownTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			r := req
			switch {
			case len(args) == 1 && r.Merged:
				return &usageError{errors.New("name a branch or give --merged, not both")}
			case len(args) == 1:
				r.Ref = args[0]
			case !r.Merged:
				return &usageError{errors.New("name a branch, or give --merged")}
			}
			list, err := client.NewBranches(a.client).Clean(ctx, r)
			if err != nil {
				return err
			}
			return a.emit(list, func() error {
				if len(list) == 0 {
					_, err := fmt.Fprintln(a.out, "no merged branches")
					return err
				}
				for _, b := range list {
					fmt.Fprintf(a.out, "%s: %s\n", b.Name, b.State)
					if b.Saved != "" {
						fmt.Fprintf(a.out, "  uncommitted work saved in %s (git checkout %s -- .)\n", b.Saved, b.Saved)
					}
				}
				return nil
			})
		}),
	})
	cmd.Flags().BoolVar(&req.Merged, "merged", false, "every merged branch")
	cmd.Flags().BoolVarP(&req.Force, "force", "f", false, "also with uncommitted changes (saved under refs/hive/saved/ first)")
	cmd.Flags().BoolVar(&req.DeleteBranch, "delete-branch", false, "also delete merged branches git agrees have merged")
	return cmd
}
