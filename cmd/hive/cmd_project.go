package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/project"
	"github.com/admirable-oss/hive/internal/protocol"
)

// upTimeout bounds hive up and down, which start and stop agents.
const upTimeout = 3 * time.Minute

const projectTimeout = 30 * time.Second

func newUpCmd(a *app) *cobra.Command {
	var plan bool
	cmd := needsDaemon(&cobra.Command{
		Use:   "up [dir]",
		Short: "Start the project in hive.toml: its environments, tabs and agents",
		Long: `Make the running workspace match the project file (hive.toml) in dir, or
in the nearest directory above it: create its environments, set their
variables, open its tabs and start its agents. What already runs is left
alone, so running it again changes nothing.

A project file runs only once you trust its current text. The first hive up,
and the first after the file changes, shows what the file declares and asks
(--yes answers yes). --plan shows what would be done and does nothing.

Variables the file declares in [env] take their values from hive.local.toml
or from your shell.`,
		Example: `  hive up
  hive up --plan
  hive up ~/src/shop --yes`,
		Args: rangeArgs(0, 1),
		RunE: withTimeout(upTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			dir, err := a.projectDir(args)
			if err != nil {
				return err
			}
			p, err := a.loadProject(ctx, dir)
			if err != nil {
				return err
			}
			vars := projectVars(p.Spec, a.getenv)
			projects := client.NewProjects(a.client)
			if plan {
				pl, err := projects.Plan(ctx, dir, false, vars)
				if err != nil {
					return projectErr(err)
				}
				return a.emitPlan(pl)
			}
			if !p.Trusted {
				if p, err = a.trustProject(ctx, p, vars); err != nil {
					return err
				}
			}
			res, err := projects.Up(ctx, dir, vars)
			if err != nil {
				if len(res.Plan.Problems) == 0 {
					return projectErr(err)
				}
				return a.emitApplied(res, p, err)
			}
			return a.emitApplied(res, p, nil)
		}),
	})
	cmd.Flags().BoolVar(&plan, "plan", false, "show what would be done, and do nothing")
	return cmd
}

func newDownCmd(a *app) *cobra.Command {
	var plan bool
	cmd := needsDaemon(&cobra.Command{
		Use:   "down [dir]",
		Short: "Stop the project's agents and close its tabs; keep its environments and worktrees",
		Long: `Stop the agents the project file (hive.toml) declares and close the tabs it
declares. Its environments stay, and so do the worktrees its agents work in,
with any uncommitted work in them. hive up starts them again.`,
		Example: `  hive down
  hive down --plan`,
		Args: rangeArgs(0, 1),
		RunE: withTimeout(upTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			dir, err := a.projectDir(args)
			if err != nil {
				return err
			}
			p, err := a.loadProject(ctx, dir)
			if err != nil {
				return err
			}
			projects := client.NewProjects(a.client)
			if plan {
				pl, err := projects.Plan(ctx, dir, true, nil)
				if err != nil {
					return projectErr(err)
				}
				return a.emitPlan(pl)
			}
			res, err := projects.Down(ctx, dir)
			if err != nil {
				return projectErr(err)
			}
			return a.emitApplied(res, p, nil)
		}),
	})
	cmd.Flags().BoolVar(&plan, "plan", false, "show what would be stopped, and do nothing")
	return cmd
}

func newProjectCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "project",
		Aliases: []string{"projects"},
		Short:   "Check, show and trust project files (hive.toml)",
		Long: `A project file, hive.toml at the root of a repository, declares the Hive
setup the repository is worked with: environments, their tabs and panes, the
agents they start and the variables they need. hive up starts it, and hive
down stops it. hive.local.toml beside it, which git should ignore, holds your
own values and preferences.

A project file runs only while it has the text you trusted.`,
		Example: `  hive project check
  hive project show
  hive project trust
  hive project list`,
	}
	cmd.AddCommand(newProjectCheckCmd(a), newProjectShowCmd(a), newProjectTrustCmd(a), newProjectListCmd(a))
	return cmd
}

func newProjectCheckCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "check [dir]",
		Short: "Check a project file without a daemon, for CI and editors",
		Long: `Check hive.toml (and hive.local.toml) in dir, or in the nearest directory
above it, as hive up would read it. It needs no daemon and no trust, and
exits 1 when the file does not check.`,
		Args: rangeArgs(0, 1),
		RunE: func(_ *cobra.Command, args []string) error {
			dir, err := absDir(cmpStr(firstArg(args), "."))
			if err != nil {
				return err
			}
			root, err := project.Find(dir)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(filepath.Join(root, project.FileName))
			if err != nil {
				return err
			}
			local, err := os.ReadFile(filepath.Join(root, project.LocalFileName))
			if errors.Is(err, fs.ErrNotExist) {
				local, err = nil, nil
			}
			if err != nil {
				return err
			}
			spec, _, err := project.Parse(data, local, root)
			if err != nil {
				return err
			}
			p := project.Project{Root: root, File: filepath.Join(root, project.FileName), Spec: &spec}
			return a.emit(spec, func() error {
				fmt.Fprintf(a.out, "%s checks: %s\n", p.File, countSpec(spec))
				return nil
			})
		},
	}
}

func newProjectShowCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "show [dir]",
		Short: "Show what a project file declares, and whether it is trusted",
		Args:  rangeArgs(0, 1),
		RunE: withTimeout(projectTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			dir, err := a.projectDir(args)
			if err != nil {
				return err
			}
			p, err := client.NewProjects(a.client).Get(ctx, dir)
			if err != nil {
				return projectErr(err)
			}
			return a.emit(p, func() error {
				if p.Error != "" {
					fmt.Fprintf(a.out, "%s does not check: %s\n", p.File, p.Error)
					return nil
				}
				describeProject(a.out, p)
				fmt.Fprintf(a.out, "trusted: %s\n", trustText(p))
				return nil
			})
		}),
	})
}

func newProjectTrustCmd(a *app) *cobra.Command {
	var revoke bool
	cmd := needsDaemon(&cobra.Command{
		Use:   "trust [dir]",
		Short: "Trust a project file at its current text, so hive up runs it",
		Long: `Trust the project file in dir (or above it) at the text it has now: hive up
runs it while it keeps that text. A change to the file, such as a pull that
edits it, needs trusting again. Read it first: hive project show. --revoke
takes the trust back.`,
		Args: rangeArgs(0, 1),
		RunE: withTimeout(projectTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			dir, err := a.projectDir(args)
			if err != nil {
				return err
			}
			p, err := client.NewProjects(a.client).Trust(ctx, dir, revoke)
			if err != nil {
				return projectErr(err)
			}
			if revoke {
				return a.done(map[string]any{"project": p}, "%s is no longer trusted", p.File)
			}
			return a.done(map[string]any{"project": p}, "%s runs while it keeps this text (sha256 %s)", p.File, shortHash(p.Digest))
		}),
	})
	cmd.Flags().BoolVar(&revoke, "revoke", false, "take the trust back")
	return cmd
}

func newProjectListCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the trusted projects",
		Args:    noArgs,
		RunE: withTimeout(projectTimeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			ps, err := client.NewProjects(a.client).List(ctx)
			if err != nil {
				return projectErr(err)
			}
			return a.emit(ps, func() error {
				if len(ps) == 0 {
					fmt.Fprintln(a.out, "no trusted projects (hive up asks to trust one)")
					return nil
				}
				tw := a.table()
				fmt.Fprintln(tw, "NAME\tTRUSTED\tROOT")
				for _, p := range ps {
					name := "-"
					if p.Spec != nil {
						name = p.Spec.Name
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\n", name, trustText(p), p.Root)
				}
				return tw.Flush()
			})
		}),
	})
}

// projectDir is the directory a project command names: the argument or
// the current directory, absolute on the daemon's machine.
func (a *app) projectDir(args []string) (string, error) {
	return a.dirArg(cmpStr(firstArg(args), "."))
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// loadProject reads the project and refuses one that does not check.
func (a *app) loadProject(ctx context.Context, dir string) (project.Project, error) {
	p, err := client.NewProjects(a.client).Get(ctx, dir)
	if err != nil {
		return project.Project{}, projectErr(err)
	}
	if p.Error != "" {
		return project.Project{}, errors.New(p.Error)
	}
	return p, nil
}

// trustProject shows an untrusted project and its plan, and trusts it
// when the person says yes.
func (a *app) trustProject(ctx context.Context, p project.Project, vars map[string]string) (project.Project, error) {
	projects := client.NewProjects(a.client)
	pl, err := projects.Plan(ctx, p.Root, false, vars)
	if err != nil {
		return p, projectErr(err)
	}
	if !a.yes {
		w := a.errOut
		fmt.Fprintf(w, "%s is not trusted at its current text. It declares:\n\n", p.File)
		describeProject(w, p)
		fmt.Fprintln(w, "\nhive up would:")
		printProjectPlan(w, pl)
		fmt.Fprintln(w)
	}
	if !a.confirm("Trust this project file at this text, and bring it up?") {
		return p, fmt.Errorf("%s is not trusted: read it, then run `hive project trust` or `hive up --yes`", p.File)
	}
	if p, err = projects.Trust(ctx, p.Root, false); err != nil {
		return p, projectErr(err)
	}
	return p, nil
}

// projectVars are the shell's values for the names the project declares,
// and nothing else.
func projectVars(spec *project.Spec, getenv func(string) string) map[string]string {
	vars := map[string]string{}
	if spec == nil {
		return vars
	}
	for _, e := range spec.Environments {
		for _, n := range append(append([]string{}, e.Required...), e.Optional...) {
			if v := getenv(n); v != "" {
				vars[n] = v
			}
		}
	}
	return vars
}

func (a *app) emitPlan(pl project.Plan) error {
	err := a.emit(pl, func() error {
		printProjectPlan(a.out, pl)
		return nil
	})
	if err == nil && len(pl.Problems) > 0 {
		return errors.New("the plan has problems: nothing would be changed")
	}
	return err
}

func (a *app) emitApplied(res project.Applied, p project.Project, failed error) error {
	err := a.emit(res, func() error {
		name := p.Root
		if p.Spec != nil {
			name = p.Spec.Name
		}
		if failed == nil && len(res.Plan.Actions) == 0 {
			if res.Plan.Down {
				fmt.Fprintf(a.out, "nothing to stop: %s is down\n", name)
			} else {
				fmt.Fprintf(a.out, "nothing to do: %s is up (%d in place)\n", name, res.Plan.Present)
			}
			return nil
		}
		printProjectPlan(a.out, project.Plan{Actions: res.Plan.Actions[:res.Done], Problems: res.Plan.Problems})
		if failed == nil {
			verb := "up"
			if res.Plan.Down {
				verb = "down"
			}
			fmt.Fprintf(a.out, "%s is %s\n", name, verb)
		}
		return nil
	})
	if failed != nil {
		return projectErr(failed)
	}
	return err
}

// printProjectPlan writes one line a step: + creates, ~ changes, - stops, and !
// is a problem.
func printProjectPlan(w io.Writer, pl project.Plan) {
	for _, act := range pl.Actions {
		fmt.Fprintln(w, actionText(act))
	}
	for _, p := range pl.Problems {
		fmt.Fprintf(w, "! %s\n", p)
	}
	if len(pl.Actions) == 0 && len(pl.Problems) == 0 {
		if pl.Down {
			fmt.Fprintln(w, "nothing to stop")
		} else {
			fmt.Fprintf(w, "nothing to do (%d in place)\n", pl.Present)
		}
	}
}

func actionText(act project.Action) string {
	at := act.Environment + "/" + act.Name
	switch act.Op {
	case project.OpCreateEnvironment:
		s := fmt.Sprintf("+ environment %s at %s", act.Environment, act.Root)
		if len(act.Vars) > 0 {
			s += ", with " + strings.Join(act.Vars, ", ")
		}
		return s
	case project.OpSetVars:
		return fmt.Sprintf("~ environment %s: set %s", act.Environment, strings.Join(act.Vars, ", "))
	case project.OpCreateTab:
		return fmt.Sprintf("+ tab %s: %s", at, commandsText(act.Commands))
	case project.OpStartAgent:
		where := ""
		switch {
		case act.In != "":
			where = ", in its worktree on " + act.Worktree + " (" + act.In + ")"
		case act.Worktree != "":
			where = ", in a worktree on " + act.Worktree
		case act.Isolate:
			where = ", in a worktree of its own"
		}
		return fmt.Sprintf("+ agent %s: %s%s", at, act.Kind, where)
	case project.OpRestartAgent:
		return fmt.Sprintf("~ agent %s: start %s again (it exited)", at, act.Kind)
	case project.OpStopAgent:
		return fmt.Sprintf("- agent %s (%s)", at, act.Kind)
	case project.OpCloseTab:
		return fmt.Sprintf("- tab %s", at)
	}
	return fmt.Sprintf("? %s %s", act.Op, at)
}

func commandsText(cmds []string) string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = cmpStr(c, "a shell")
	}
	return strings.Join(out, " | ")
}

// describeProject writes everything a project file would run, for a
// person deciding whether to trust it.
func describeProject(w io.Writer, p project.Project) {
	if p.Spec == nil {
		return
	}
	fmt.Fprintf(w, "project %s (%s)\n", p.Spec.Name, p.File)
	if p.Spec.Local {
		fmt.Fprintf(w, "  with %s\n", project.LocalFileName)
	}
	for _, e := range p.Spec.Environments {
		fmt.Fprintf(w, "  environment %s at %s\n", e.ID, e.Path)
		if len(e.Vars) > 0 {
			fmt.Fprintf(w, "    vars: %s\n", strings.Join(slices.Sorted(maps.Keys(e.Vars)), ", "))
		}
		if len(e.Required)+len(e.Optional) > 0 {
			needs := slices.Clone(e.Required)
			for _, n := range e.Optional {
				needs = append(needs, n+" (optional)")
			}
			fmt.Fprintf(w, "    needs: %s\n", strings.Join(needs, ", "))
		}
		for _, t := range e.Tabs {
			fmt.Fprintf(w, "    tab %s:\n", t.Name)
			for _, pn := range t.Panes {
				line := cmpStr(project.CommandLine(pn.Command), "a shell")
				if pn.Cwd != "" {
					line += " (in " + pn.Cwd + ")"
				}
				if pn.Name != "" {
					line = pn.Name + ": " + line
				}
				fmt.Fprintf(w, "      %s\n", line)
			}
		}
		for _, ag := range e.Agents {
			line := fmt.Sprintf("    agent %s: %s", ag.Name, strings.Join(ag.Kinds, " or "))
			switch {
			case ag.Worktree != "":
				line += ", in a worktree on " + ag.Worktree
			case ag.Isolate:
				line += ", in a worktree of its own"
			}
			if len(ag.Args) > 0 {
				line += ", with " + project.CommandLine(ag.Args)
			}
			if ag.Skip {
				line += " (not started here: " + project.LocalFileName + ")"
			}
			fmt.Fprintln(w, line)
		}
	}
}

func countSpec(s project.Spec) string {
	tabs, agents := 0, 0
	for _, e := range s.Environments {
		tabs += len(e.Tabs)
		agents += len(e.Agents)
	}
	count := func(n int, one string) string { return fmt.Sprintf("%d %s", n, plural(n, one, one+"s")) }
	return fmt.Sprintf("project %s, %s, %s, %s", s.Name,
		count(len(s.Environments), "environment"), count(tabs, "tab"), count(agents, "agent"))
}

func trustText(p project.Project) string {
	if p.Trusted {
		return "yes, at sha256 " + shortHash(p.Digest)
	}
	return "no"
}

func projectErr(err error) error {
	if pe, ok := errors.AsType[*protocol.Error](err); ok && pe.Code == protocol.ErrorCodeUnknownMethod {
		return fmt.Errorf("the hive daemon is too old for projects; restart it with `hive daemon restart` (%w)", err)
	}
	return err
}
