//go:build !windows

package runtime_test

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/plugin"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/runtime"
)

const scopedManifest = `api = 1
id = "scoped"
version = "1.0.0"

[permissions]
required = ["hive.read"]

[[actions]]
id = "where"
command = ["sh", "-c", "printf %s \"$HIVE_SOCKET_PATH\" > \"$HIVE_PLUGIN_DATA/socket\"; printf %s \"$HIVE_PLUGIN_CAPABILITIES\""]

[[contributes.status]]
id = "state"
title = "State"
`

// TestPluginSocketIsScoped: a plugin reaches the daemon on its own socket,
// which offers and allows only what its capabilities cover, and on which
// it can act only on itself.
func TestPluginSocketIsScoped(t *testing.T) {
	sock := shortSock(t)
	root := filepath.Dir(sock)
	dir := filepath.Join(t.TempDir(), "scoped")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, plugin.ManifestFile), []byte(scopedManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	store := plugin.NewStore(filepath.Join(root, "plugins"))
	if _, _, err := store.Link(context.Background(), dir, "dev"); err != nil {
		t.Fatal(err)
	}
	start(t, sock)
	c := client.NewService(client.Config{SocketPath: sock})
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var res plugin.Result
	for {
		var err error
		res, err = client.NewPlugins(c).Run(ctx, client.PluginRun{Plugin: "scoped", Action: "where"})
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if res.Output != "hive.read" {
		t.Fatalf("capabilities seen by the plugin: %q", res.Output)
	}
	path, err := os.ReadFile(filepath.Join(store.DataDir("scoped"), "socket"))
	if err != nil || string(path) == sock || len(path) == 0 {
		t.Fatalf("plugin socket %q (daemon's %q): %v", path, sock, err)
	}

	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", string(path))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := protocol.Handshake(ctx, conn, protocol.Hello{Client: "plugin/scoped"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer mux.Close()
	methods := mux.Welcome().Methods
	if !slices.Contains(methods, "process.list") || slices.Contains(methods, "process.start") || slices.Contains(methods, "plugin.run") || !slices.Contains(methods, "plugin.status") {
		t.Fatalf("welcome methods = %v", methods)
	}
	if err := mux.Call(ctx, "process.list", struct{}{}, nil); err != nil {
		t.Fatalf("a granted read: %v", err)
	}
	err = mux.Call(ctx, "process.start", map[string]any{"environment_id": "x", "command": "true"}, nil)
	if pe, ok := errors.AsType[*protocol.Error](err); !ok || pe.Code != protocol.ErrorCodePermissionDenied || !strings.Contains(pe.Message, "hive.process") {
		t.Fatalf("an ungranted call: %v", err)
	}
	var st plugin.StatusContent
	if err := mux.Call(ctx, "plugin.status", map[string]any{"indicator": "state", "text": "ok"}, &st); err != nil || st.Plugin != "scoped" {
		t.Fatalf("its own indicator: %+v %v", st, err)
	}
	err = mux.Call(ctx, "plugin.status", map[string]any{"plugin": "other", "indicator": "state", "text": "x"}, nil)
	if pe, ok := errors.AsType[*protocol.Error](err); !ok || pe.Code != protocol.ErrorCodePermissionDenied {
		t.Fatalf("acting as another plugin: %v", err)
	}
	var d plugin.Discovery
	if err := mux.Call(ctx, "plugin.capabilities", struct{}{}, &d); err != nil || d.Plugin != "scoped" || !slices.Equal(d.Granted, []string{"hive.read"}) || d.API != 1 { // its own plugin API
		t.Fatalf("discovery = %+v, %v", d, err)
	}

	// The daemon's own socket sees the status it set, and its health.
	var list []plugin.Status
	if err := mux.Call(ctx, "plugin.list", struct{}{}, &list); err != nil || len(list) != 1 || list[0].Health.State == "" {
		t.Fatalf("plugin.list from the plugin: %+v, %v", list, err)
	}
	var all plugin.Contributions
	if err := c.Call(ctx, "plugin.contributions", nil, &all); err != nil || len(all.Status) != 1 || all.Status[0].Text != "ok" {
		t.Fatalf("contributions = %+v, %v", all, err)
	}
}

// TestStrictPluginReachesOnlyItsSocket: under strict isolation a plugin
// connects to its own socket, and not to the daemon's.
func TestStrictPluginReachesOnlyItsSocket(t *testing.T) {
	if !plugin.SandboxAvailable() || goruntime.GOOS != "darwin" {
		t.Skip("strict isolation is exercised on macOS")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("the probe needs python3")
	}
	sock := shortSock(t)
	root := filepath.Dir(sock)
	dir := filepath.Join(t.TempDir(), "boxed")
	_ = os.MkdirAll(dir, 0o755)
	manifest := `api = 1
id = "boxed"
version = "1.0.0"
isolation = "strict"
[permissions]
required = ["hive.read"]
[[actions]]
id = "probe"
command = ["python3", "-c", "import os,socket\nfor name, p in [('own', os.environ['HIVE_SOCKET_PATH']), ('daemon', os.path.join(os.path.dirname(os.path.dirname(os.environ['HIVE_SOCKET_PATH'])), 'hive.sock'))]:\n    c = socket.socket(socket.AF_UNIX)\n    try:\n        c.connect(p); print(name + '-reached')\n    except OSError as e:\n        print(name + '-refused', e)\n"]
`
	if err := os.WriteFile(filepath.Join(dir, plugin.ManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := plugin.NewStore(filepath.Join(root, "plugins")).Link(context.Background(), dir, "dev"); err != nil {
		t.Fatal(err)
	}
	mod := runtime.NewModule(runtime.Config{SocketPath: sock, InheritEnv: true})
	if err := mod.Service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mod.Service.Stop(context.Background()) })
	c := client.NewService(client.Config{SocketPath: sock})
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var res plugin.Result
	for {
		var err error
		if res, err = client.NewPlugins(c).Run(ctx, client.PluginRun{Plugin: "boxed", Action: "probe"}); err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(res.Output, "own-reached") || !strings.Contains(res.Output, "daemon-refused") {
		t.Fatalf("strict plugin output:\n%s", res.Output)
	}
}
