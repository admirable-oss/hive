package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
)

func cmdEnvironment(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing environment command (list, create, get, remove)")
	}
	switch args[0] {
	case "list":
		return cmdEnvironmentList(ctx, args[1:])
	case "create":
		return cmdEnvironmentCreate(ctx, args[1:])
	case "get":
		return cmdEnvironmentGet(ctx, args[1:])
	case "remove", "rm":
		return cmdEnvironmentRemove(ctx, args[1:])
	default:
		return fmt.Errorf("unknown environment command %q", args[0])
	}
}

func cmdEnvironmentList(ctx context.Context, args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	envs, err := c.EnvironmentList(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tSTATUS")
	for _, env := range envs {
		fmt.Fprintf(w, "%s\t%s\t%s\n", env.ID, env.Name, env.Status)
	}
	return w.Flush()
}

func cmdEnvironmentCreate(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing environment id")
	}
	id := args[0]
	c, err := newClient()
	if err != nil {
		return err
	}
	_, err = c.EnvironmentCreate(ctx, id)
	if err != nil {
		return err
	}
	fmt.Printf("created environment %q\n", id)
	return nil
}

func cmdEnvironmentGet(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing environment id")
	}
	id := args[0]
	c, err := newClient()
	if err != nil {
		return err
	}
	env, err := c.EnvironmentGet(ctx, id)
	if err != nil {
		return err
	}
	
	fmt.Println("Environment")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintf(w, "  ID\t%s\n", env.ID)
	fmt.Fprintf(w, "  Name\t%s\n", env.Name)
	fmt.Fprintf(w, "  Status\t%s\n", env.Status)
	fmt.Fprintf(w, "  Path\t%s\n", env.Path)
	fmt.Fprintf(w, "  Created\t%s\n", env.CreatedAt.Format("2006-01-02 15:04:05"))
	return w.Flush()
}

func cmdEnvironmentRemove(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing environment id")
	}
	id := args[0]
	c, err := newClient()
	if err != nil {
		return err
	}
	err = c.EnvironmentRemove(ctx, id)
	if err != nil {
		return err
	}
	fmt.Printf("removed environment %q\n", id)
	return nil
}
