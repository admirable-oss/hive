// Package daemonctl starts and installs the Hive daemon from the client
// side: launching it detached from the terminal (autostart), and installing
// it as a per-user launchd agent (macOS) or systemd user service (Linux) so
// it starts at login and is restarted if it crashes.
//
// It knows nothing about the wire protocol; callers pass a readiness probe.
package daemonctl
