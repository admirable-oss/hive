package main

import (
	"context"
	"fmt"
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
	case "remove":
		return cmdEnvironmentRemove(ctx, args[1:])
	default:
		return fmt.Errorf("unknown environment command %q", args[0])
	}
}

func cmdEnvironmentList(ctx context.Context, args []string) error {
	return fmt.Errorf("not implemented")
}

func cmdEnvironmentCreate(ctx context.Context, args []string) error {
	return fmt.Errorf("not implemented")
}

func cmdEnvironmentGet(ctx context.Context, args []string) error {
	return fmt.Errorf("not implemented")
}

func cmdEnvironmentRemove(ctx context.Context, args []string) error {
	return fmt.Errorf("not implemented")
}
