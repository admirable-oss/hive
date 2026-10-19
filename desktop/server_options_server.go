//go:build server

package main

import (
	"os"
	"strconv"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const serverBuild = true

// A server build (-tags server) runs the app as an HTTP server for a
// browser, so CI can test it end to end without a display. It has full
// control of the daemon and no authentication of its own: it listens on
// localhost only, and is never released.
func serverOptions() application.ServerOptions {
	port, _ := strconv.Atoi(os.Getenv("HIVE_DESKTOP_TEST_PORT"))
	if port == 0 {
		port = 9246
	}
	return application.ServerOptions{Host: "127.0.0.1", Port: port}
}
