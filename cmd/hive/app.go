package main

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/admirable-oss/hive/internal/client"
)

// app is everything a command needs. run builds it once (the CLI's
// composition root) and passes it down, so commands never reach for globals.
type app struct {
	root   string // storage root, e.g. ~/.hive
	socket string // daemon socket inside root
	client client.Client
	out    io.Writer
}

func newApp() (*app, error) {
	root := os.Getenv("HIVE_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("locate home directory: %w", err)
		}
		root = filepath.Join(home, ".hive")
	}
	socket := filepath.Join(root, "hive.sock")
	return &app{
		root:   root,
		socket: socket,
		client: client.NewService(client.Config{SocketPath: socket}),
		out:    os.Stdout,
	}, nil
}

func (a *app) table() *tabwriter.Writer {
	return tabwriter.NewWriter(a.out, 0, 0, 4, ' ', 0)
}

// subcommands maps a command group's verbs to their handlers.
type subcommands map[string]func(ctx context.Context, a *app, args []string) error

func (s subcommands) dispatch(ctx context.Context, a *app, group string, args []string) error {
	verbs := slices.Sorted(maps.Keys(s))
	if len(args) == 0 {
		return fmt.Errorf("usage: hive %s <%s>", group, strings.Join(verbs, "|"))
	}
	fn, ok := s[args[0]]
	if !ok {
		return fmt.Errorf("unknown %s command %q (want %s)", group, args[0], strings.Join(verbs, ", "))
	}
	return fn(ctx, a, args[1:])
}

// need returns an error unless args has at least n entries.
func need(args []string, n int, usage string) error {
	if len(args) < n {
		return fmt.Errorf("usage: hive %s", usage)
	}
	return nil
}
