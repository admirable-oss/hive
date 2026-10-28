//go:build !windows

package plugin

// The launcher of a sandboxed plugin API 2 command on Linux: bubblewrap
// runs `hive __plugin-sandbox -- <command>` inside the sandbox, and it
// limits which programs may run with Landlock (5.13 and later), forwards
// the plugin's proxy into the sandbox's network namespace when the plugin
// lists hosts, and runs the command.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

// SandboxMain is `hive __plugin-sandbox -- <command...>`.
func SandboxMain(args []string) int {
	ctx := context.Background()
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(os.Stderr, "hive: plugin sandbox: "+format+"\n", a...)
		return 126
	}
	if len(args) == 0 {
		return fail("no command")
	}
	var spec launchSpec
	if err := json.Unmarshal([]byte(os.Getenv(launchEnv)), &spec); err != nil {
		return fail("no launch spec: %v", err)
	}
	env := environWithout(os.Environ(), launchEnv)
	if !spec.ExecAny {
		if err := restrictExec(spec.Exec); err != nil {
			if !errors.Is(err, ErrNoLandlock) {
				return fail("%v", err)
			}
			fmt.Fprintln(os.Stderr, "hive: plugin sandbox: this kernel has no Landlock (5.13 or later), so the programs this plugin runs are not limited")
		}
	}
	path, err := exec.LookPath(args[0])
	if err != nil {
		return fail("%v", err)
	}
	if spec.ProxySocket == "" {
		err := syscall.Exec(path, args, env) //nolint:gosec // running the plugin's command is the launcher's job; the sandbox around it is the boundary
		return fail("exec %s: %v", args[0], err)
	}
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return fail("the network proxy: %v", err)
	}
	defer l.Close()
	go forwardProxy(ctx, l, spec.ProxySocket)
	url := "http://" + proxyUser + ":" + spec.ProxyToken + "@127.0.0.1:" + strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	cmd := exec.CommandContext(ctx, path, args[1:]...) //nolint:gosec // as above: the plugin's command, inside its sandbox
	cmd.Args[0] = args[0]
	cmd.Env = append(environWithout(env, "HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY", "https_proxy", "http_proxy", "all_proxy", "no_proxy"), ProxyEnv(url)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return fail("start %s: %v", args[0], err)
	}
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		for s := range sigs {
			_ = cmd.Process.Signal(s)
		}
	}()
	err = cmd.Wait()
	signal.Stop(sigs)
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	if err != nil {
		return fail("%v", err)
	}
	return 0
}

// environWithout is env without the named variables.
func environWithout(env []string, names ...string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, n := range names {
			if k == n {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}
