// Package pluginsdk is the supported Go SDK for Hive plugin processes.
//
// It reads Hive's invocation context and connects to the host daemon through
// pkg/hiveapi. It never imports Hive internals and uses the same protocol
// methods as the TypeScript, Rust and Python SDKs.
package pluginsdk

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// InitOptions controls plugin initialization. A nil Input means os.Stdin.
type InitOptions struct {
	Input io.Reader
	Hive  hiveapi.Options
}

// Plugin is the initialized process context and Hive daemon client.
type Plugin struct {
	Context Context
	Hive    *hiveapi.Client
}

// Init reads the invocation and opens a daemon connection. Call Close when done.
func Init(ctx context.Context, opts InitOptions) (*Plugin, error) {
	input := opts.Input
	if input == nil {
		input = os.Stdin
	}
	call, err := ReadContext(input)
	if err != nil {
		return nil, err
	}
	if opts.Hive.Name == "" {
		opts.Hive.Name = "plugin/" + call.Plugin
	}
	api, err := hiveapi.ConnectContext(ctx, opts.Hive)
	if err != nil {
		return nil, fmt.Errorf("connect to Hive: %w", err)
	}
	return &Plugin{Context: call, Hive: api}, nil
}

// Close releases the daemon connection.
func (p *Plugin) Close() error {
	if p == nil || p.Hive == nil {
		return nil
	}
	return p.Hive.Close()
}

// Handler receives one plugin invocation and a connected Hive API client.
type Handler func(context.Context, Context, *hiveapi.Client) error

// Run initializes a plugin, calls handler, and closes the connection.
func Run(ctx context.Context, handler Handler) error {
	return RunWithOptions(ctx, InitOptions{}, handler)
}

// RunWithOptions is Run with testable/custom invocation input and connection options.
func RunWithOptions(ctx context.Context, opts InitOptions, handler Handler) error {
	if handler == nil {
		return fmt.Errorf("run Hive plugin: handler is nil")
	}
	p, err := Init(ctx, opts)
	if err != nil {
		return err
	}
	defer p.Close()
	return handler(ctx, p.Context, p.Hive)
}
