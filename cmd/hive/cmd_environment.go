package main

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/git"
)

func newEnvironmentCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "environment",
		Aliases: []string{"env"},
		Short:   "Manage environments (the directories agents run in)",
	}
	const timeout = 15 * time.Second
	cmd.AddCommand(
		needsDaemon(&cobra.Command{
			Use:     "list",
			Aliases: []string{"ls"},
			Short:   "List environments",
			Args:    noArgs,
			RunE: withTimeout(timeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
				envs, err := a.client.EnvironmentList(ctx)
				if err != nil {
					return err
				}
				return a.emit(envs, func() error {
					w := a.table()
					fmt.Fprintln(w, "ID\tSTATUS\tGIT\tCREATED\tPATH")
					for _, env := range envs {
						fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", env.ID, env.Status, gitSummary(env.Git), formatTime(env.CreatedAt), env.Path)
					}
					return w.Flush()
				})
			}),
		}),
		newEnvironmentCreateCmd(a),
		needsDaemon(&cobra.Command{
			Use:               "get <id>",
			Short:             "Show an environment",
			Args:              exactArgs(1),
			ValidArgsFunction: completeEnvironments(a),
			RunE: withTimeout(timeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
				env, err := a.client.EnvironmentGet(ctx, args[0])
				if err != nil {
					return err
				}
				return a.emit(env, func() error { return printEnvironment(a, env) })
			}),
		}),
		needsDaemon(&cobra.Command{
			Use:               "set <id> KEY=VALUE...",
			Short:             "Set variables every new process in the environment gets",
			Args:              minArgs(2),
			ValidArgsFunction: completeEnvironments(a),
			RunE: withTimeout(timeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
				vars, err := parseVars(args[1:])
				if err != nil {
					return err
				}
				env, err := a.client.EnvironmentUpdate(ctx, environment.UpdateRequest{ID: args[0], Set: vars})
				if err != nil {
					return err
				}
				return a.emit(env, func() error {
					fmt.Fprintf(a.out, "set %s in environment %q (new processes get them)\n", strings.Join(slices.Sorted(maps.Keys(vars)), ", "), env.ID)
					return nil
				})
			}),
		}),
		needsDaemon(&cobra.Command{
			Use:               "unset <id> KEY...",
			Short:             "Remove environment variables",
			Args:              minArgs(2),
			ValidArgsFunction: completeEnvironments(a),
			RunE: withTimeout(timeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
				env, err := a.client.EnvironmentUpdate(ctx, environment.UpdateRequest{ID: args[0], Unset: args[1:]})
				if err != nil {
					return err
				}
				return a.emit(env, func() error {
					fmt.Fprintf(a.out, "unset %s in environment %q\n", strings.Join(args[1:], ", "), env.ID)
					return nil
				})
			}),
		}),
		needsDaemon(&cobra.Command{
			Use:     "remove <id>",
			Aliases: []string{"rm"},
			Short:   "Stop an environment's agents and remove it",
			Long: `Stop an environment's agents and remove it. A workspace Hive created
(--managed) is deleted with it; a directory of yours never is.`,
			Args:              exactArgs(1),
			ValidArgsFunction: completeEnvironments(a),
			RunE: withTimeout(timeout+a.cfg.Daemon.ShutdownTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
				if err := a.client.EnvironmentRemove(ctx, args[0]); err != nil {
					return err
				}
				return a.done(map[string]any{"id": args[0]}, "removed environment %q", args[0])
			}),
		}),
	)
	return cmd
}

func newEnvironmentCreateCmd(a *app) *cobra.Command {
	var (
		cwd     string
		managed bool
		vars    []string
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "create <id>",
		Short: "Create an environment for a directory (the current one by default)",
		Long: `Create an environment. Its agents run in an existing directory: the current
one, or the one given with --cwd. With --managed, Hive makes an empty
workspace for it instead (deleted with the environment).`,
		Example: `  hive env create api                 # agents run in the current directory
  hive env create web --cwd ~/src/web --env PORT=3000
  hive env create scratch --managed`,
		Args: exactArgs(1),
		RunE: withTimeout(15*time.Second, func(ctx context.Context, cmd *cobra.Command, args []string) error {
			req := environment.CreateRequest{ID: args[0]}
			if managed && cmd.Flags().Changed("cwd") {
				return &usageError{fmt.Errorf("--managed and --cwd exclude each other")}
			}
			if !managed {
				root, err := absDir(cwd)
				if err != nil {
					return err
				}
				req.Root = root
			}
			env, err := parseVars(vars)
			if err != nil {
				return err
			}
			req.Env = env
			created, err := a.client.EnvironmentCreate(ctx, req)
			if err != nil {
				return err
			}
			return a.emit(created, func() error {
				fmt.Fprintf(a.out, "created environment %q at %s\n", created.ID, created.Path)
				return nil
			})
		}),
	})
	cmd.Flags().StringVar(&cwd, "cwd", ".", "the directory agents run in")
	cmd.Flags().BoolVar(&managed, "managed", false, "run agents in a new empty workspace instead of a directory of yours")
	cmd.Flags().StringArrayVarP(&vars, "env", "e", nil, "a variable for every process, as KEY=VALUE (repeatable)")
	return cmd
}

func printEnvironment(a *app, env environment.Environment) error {
	w := a.table()
	fmt.Fprintf(w, "ID\t%s\n", env.ID)
	fmt.Fprintf(w, "Status\t%s\n", env.Status)
	kind := "your directory"
	if env.Managed {
		kind = "managed workspace (deleted with the environment)"
	}
	fmt.Fprintf(w, "Path\t%s\n", env.Path)
	fmt.Fprintf(w, "Kind\t%s\n", kind)
	if env.Worktree != nil {
		fmt.Fprintf(w, "Worktree\t%s of %s\n", env.Worktree.Branch, env.Worktree.Repo)
	}
	if env.Git != nil {
		fmt.Fprintf(w, "Git\t%s\n", gitSummary(env.Git))
	}
	for _, k := range slices.Sorted(maps.Keys(env.Env)) {
		fmt.Fprintf(w, "Env\t%s=%s\n", k, env.Env[k])
	}
	fmt.Fprintf(w, "Created\t%s\n", formatTime(env.CreatedAt))
	return w.Flush()
}

// gitSummary is a short status: "main ↑1 ↓2 *3" (3 changed paths).
func gitSummary(st *git.Status) string {
	if st == nil {
		return "-"
	}
	s := st.Branch
	if st.Detached || s == "" {
		s = "(" + cmpStr(st.Head, "no commits") + ")"
	}
	if st.Ahead > 0 {
		s += fmt.Sprintf(" ↑%d", st.Ahead)
	}
	if st.Behind > 0 {
		s += fmt.Sprintf(" ↓%d", st.Behind)
	}
	if n := st.Staged + st.Modified + st.Untracked + st.Conflicts; n > 0 {
		s += fmt.Sprintf(" *%d", n)
	}
	return s
}

func cmpStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// parseVars reads KEY=VALUE arguments.
func parseVars(args []string) (map[string]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(args))
	for _, arg := range args {
		k, v, ok := strings.Cut(arg, "=")
		if !ok || k == "" {
			return nil, &usageError{fmt.Errorf("%q is not KEY=VALUE", arg)}
		}
		out[k] = v
	}
	return out, nil
}

// absDir resolves a directory argument against the current directory and
// checks it exists.
func absDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	return abs, nil
}
