// Package client talks to a running Hive daemon over its unix socket. The CLI
// and the TUI depend only on the Client contract, never on the daemon's
// packages, so either side can change behind the wire protocol.
package client
