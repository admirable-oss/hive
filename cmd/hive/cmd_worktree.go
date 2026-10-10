package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
)

// worktreeTimeout allows for git checking out a large tree.
const worktreeTimeout = 2 * time.Minute

func newWorktreeCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "worktree",
		Aliases: []string{"wt"},
		Short:   "Give agents their own checkout of a repository (git worktrees)",
		Long: `Worktrees let several agents work on one repository without touching each
other's files: each gets a separate checkout of its own branch, and an
environment rooted in it. Hive puts them in ~/.hive/worktrees/<repo>/<branch>
(see worktrees.directory in the config).`,
	}
	var repo string
	list := needsDaemon(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List a repository's worktrees and their environments",
		Args:    noArgs,
		RunE: withTimeout(paneTimeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			dir, err := absDir(repo)
			if err != nil {
				return err
			}
			list, err := client.NewWorkspace(a.client).WorktreeList(ctx, dir)
			if err != nil {
				return err
			}
			return a.emit(list, func() error {
				w := a.table()
				fmt.Fprintln(w, "BRANCH\tHEAD\tENVIRONMENTS\tPATH")
				for _, wt := range list {
					branch := wt.Branch
					switch {
					case wt.Bare:
						branch = "(bare)"
					case wt.Detached:
						branch = "(detached)"
					}
					if wt.Main {
						branch += " (main)"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", branch, cmpStr(shortHash(wt.Head), "-"), cmpStr(strings.Join(wt.Environments, ","), "-"), wt.Path)
				}
				return w.Flush()
			})
		}),
	})
	list.Flags().StringVar(&repo, "repo", ".", "a directory of the repository")

	cmd.AddCommand(list, newWorktreeCreateCmd(a), newWorktreeOpenCmd(a), newWorktreeRemoveCmd(a))
	return cmd
}

func newWorktreeCreateCmd(a *app) *cobra.Command {
	var (
		req  client.WorktreeCreateRequest
		repo string
		vars []string
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "create <branch>",
		Short: "Check out a branch in a new worktree, with an environment for it",
		Long: `Check out a branch in a new worktree and create an environment rooted in it
(named after the branch unless --id is given). A branch that does not
exist yet is created from --base, or from the repository's HEAD.`,
		Example: `  hive worktree create fix/login-redirect
  hive worktree create feature/search --base origin/main --id search`,
		Args: exactArgs(1),
		RunE: withTimeout(worktreeTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			dir, err := absDir(repo)
			if err != nil {
				return err
			}
			env, err := parseVars(vars)
			if err != nil {
				return err
			}
			r := req
			r.Repo, r.Branch, r.Env = dir, args[0], env
			created, err := client.NewWorkspace(a.client).WorktreeCreate(ctx, r)
			if err != nil {
				return err
			}
			return a.emit(created, func() error {
				fmt.Fprintf(a.out, "created environment %q on branch %s at %s\n", created.ID, args[0], created.Path)
				return nil
			})
		}),
	})
	cmd.Flags().StringVar(&repo, "repo", ".", "a directory of the repository")
	cmd.Flags().StringVar(&req.Base, "base", "", "where a new branch starts (default HEAD)")
	cmd.Flags().StringVar(&req.ID, "id", "", "the environment's ID (default: from the branch)")
	cmd.Flags().StringArrayVarP(&vars, "env", "e", nil, "a variable for every process, as KEY=VALUE (repeatable)")
	return cmd
}

func newWorktreeOpenCmd(a *app) *cobra.Command {
	var req client.WorktreeOpenRequest
	cmd := needsDaemon(&cobra.Command{
		Use:   "open <path>",
		Short: "Create an environment for an existing worktree (or show its environment)",
		Args:  exactArgs(1),
		RunE: withTimeout(paneTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			dir, err := absDir(args[0])
			if err != nil {
				return err
			}
			r := req
			r.Path = dir
			env, err := client.NewWorkspace(a.client).WorktreeOpen(ctx, r)
			if err != nil {
				return err
			}
			return a.emit(env, func() error {
				fmt.Fprintf(a.out, "environment %q at %s\n", env.ID, env.Path)
				return nil
			})
		}),
	})
	cmd.Flags().StringVar(&req.ID, "id", "", "the environment's ID (default: from the branch)")
	return cmd
}

func newWorktreeRemoveCmd(a *app) *cobra.Command {
	var force bool
	cmd := needsDaemon(&cobra.Command{
		Use:     "remove <env|path>",
		Aliases: []string{"rm"},
		Short:   "Stop the agents in a worktree, remove its environments and the worktree",
		Long: `Stop the agents in a worktree, remove the environments rooted in it, then
the worktree itself. It refuses when the worktree has uncommitted changes,
unless --force. The branch is kept.`,
		Args:              exactArgs(1),
		ValidArgsFunction: completeEnvironments(a),
		RunE: withTimeout(worktreeTimeout+a.cfg.Daemon.ShutdownTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			req := client.WorktreeRemoveRequest{ID: args[0], Force: force}
			if strings.ContainsRune(args[0], filepath.Separator) || args[0] == "." || args[0] == ".." {
				if fi, err := os.Stat(args[0]); err == nil && fi.IsDir() {
					dir, err := absDir(args[0])
					if err != nil {
						return err
					}
					req = client.WorktreeRemoveRequest{Path: dir, Force: force}
				}
			}
			if err := client.NewWorkspace(a.client).WorktreeRemove(ctx, req); err != nil {
				return err
			}
			return a.done(map[string]any{"id": args[0]}, "removed worktree %s", args[0])
		}),
	})
	cmd.Flags().BoolVarP(&force, "force", "f", false, "discard uncommitted changes")
	return cmd
}

func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}
