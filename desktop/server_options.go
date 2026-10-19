//go:build !server

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// serverBuild says the app runs as an HTTP server (tests): no tray, dock or
// menus, which need the desktop's main loop.
const serverBuild = false

// serverOptions are empty in the desktop app: it serves nothing over HTTP.
func serverOptions() application.ServerOptions { return application.ServerOptions{} }
