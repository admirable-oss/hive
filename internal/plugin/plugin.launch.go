package plugin

import (
	"context"
	"io"
	"net"
)

// SandboxCommand is the hidden hive command the sandbox runs first.
const SandboxCommand = "__plugin-sandbox"

// launchEnv carries the launch spec to the launcher; it is removed before
// the command runs.
const launchEnv = "HIVE_SANDBOX_LAUNCH"

// launchSpec is what the launcher sets up.
type launchSpec struct {
	// Exec are the files and directories programs may be run from;
	// ExecAny lifts the limit.
	Exec    []string `json:"exec,omitempty"`
	ExecAny bool     `json:"exec_any,omitempty"`
	// ProxySocket is the plugin's proxy, bound into the sandbox, and
	// ProxyToken its credential.
	ProxySocket string `json:"proxy_socket,omitempty"`
	ProxyToken  string `json:"proxy_token,omitempty"`
}

// forwardProxy joins each connection on l to the proxy socket.
func forwardProxy(ctx context.Context, l net.Listener, socket string) {
	var d net.Dialer
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			up, err := d.DialContext(ctx, "unix", socket)
			if err != nil {
				return
			}
			defer up.Close()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(up, c); done <- struct{}{} }()
			go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()
			<-done
		}()
	}
}
