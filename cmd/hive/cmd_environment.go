package main

import (
	"context"
	"fmt"
)

func cmdEnvironment(ctx context.Context, a *app, args []string) error {
	return subcommands{
		"list":   envList,
		"create": envCreate,
		"get":    envGet,
		"rm":     envRemove,
		"remove": envRemove,
	}.dispatch(ctx, a, "environment", args)
}

func envList(ctx context.Context, a *app, _ []string) error {
	envs, err := a.client.EnvironmentList(ctx)
	if err != nil {
		return err
	}
	w := a.table()
	fmt.Fprintln(w, "ID\tNAME\tSTATUS")
	for _, env := range envs {
		fmt.Fprintf(w, "%s\t%s\t%s\n", env.ID, env.Name, env.Status)
	}
	return w.Flush()
}

func envCreate(ctx context.Context, a *app, args []string) error {
	if err := need(args, 1, "environment create <id>"); err != nil {
		return err
	}
	env, err := a.client.EnvironmentCreate(ctx, args[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "created environment %q at %s\n", env.ID, env.Path)
	return nil
}

func envGet(ctx context.Context, a *app, args []string) error {
	if err := need(args, 1, "environment get <id>"); err != nil {
		return err
	}
	env, err := a.client.EnvironmentGet(ctx, args[0])
	if err != nil {
		return err
	}
	fmt.Fprintln(a.out, "Environment")
	w := a.table()
	fmt.Fprintf(w, "  ID\t%s\n", env.ID)
	fmt.Fprintf(w, "  Name\t%s\n", env.Name)
	fmt.Fprintf(w, "  Status\t%s\n", env.Status)
	fmt.Fprintf(w, "  Path\t%s\n", env.Path)
	fmt.Fprintf(w, "  Created\t%s\n", env.CreatedAt.Format("2006-01-02 15:04:05"))
	return w.Flush()
}

// envRemove deletes the environment's workspace after stopping its agents.
func envRemove(ctx context.Context, a *app, args []string) error {
	if err := need(args, 1, "environment rm <id>"); err != nil {
		return err
	}
	if err := a.client.EnvironmentRemove(ctx, args[0]); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "removed environment %q\n", args[0])
	return nil
}
