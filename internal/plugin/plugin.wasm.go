package plugin

// WebAssembly plugins (plugin API 2): a command whose program is a .wasm
// file in the plugin's package runs as a WASI module inside the daemon,
// through wazero (pure Go, no cgo). The module sees its data directory as
// /data and its settings as /config, and nothing else, gets the command's context on stdin, has no
// network and cannot start processes, and calls Hive through one host
// function with the same capability checks as the plugin's socket. It
// runs the same on every system, Windows included, with no sandbox tool.
//
// The host module "hive" exports, for the guest to import:
//
//	call(req_ptr, req_len u32) u32   // performs {"method", "params"}; returns the response's length
//	response(ptr u32)                // copies that response into the guest's memory at ptr
//
// A response is {"result": …} or {"error": {"code", "message"}}.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

// Wasm limits.
const (
	// wasmMemoryPages bounds a module's memory: 1024 pages of 64 KiB.
	wasmMemoryPages = 1024
	// MaxWasmModuleBytes bounds a module file.
	MaxWasmModuleBytes = 64 << 20
	// maxWasmRequest and maxWasmResponse bound a call's JSON.
	maxWasmRequest  = 4 << 20
	maxWasmResponse = 16 << 20
)

// RuntimeWasm is a [[runtime]] kind: the plugin's commands are WebAssembly
// modules.
const RuntimeWasm = "wasm"

// SandboxWasm is how plugin.list names a Wasm plugin's confinement.
const SandboxWasm = "wasm"

// IsWasmCommand reports a command whose program is a .wasm module.
func IsWasmCommand(argv []string) bool {
	return len(argv) > 0 && strings.HasSuffix(strings.ToLower(argv[0]), ".wasm")
}

// Wasm reports a plugin whose commands are all WebAssembly modules (it
// declares [[runtime]] kind = "wasm").
func (m *Manifest) Wasm() bool {
	for _, r := range m.Runtimes {
		if r.Kind == RuntimeWasm {
			return true
		}
	}
	return false
}

// HiveCall performs a daemon method as a plugin, with its capabilities
// checked as on its socket.
type HiveCall func(ctx context.Context, plugin, method string, params json.RawMessage) (json.RawMessage, error)

// wasmHost runs Wasm plugins' modules.
type wasmHost struct {
	once    sync.Once
	ctx     context.Context // the runtime's life
	rt      wazero.Runtime
	initErr error
	mu      sync.Mutex
	modules map[string]wasmCompiled
}

type wasmCompiled struct {
	mod     wazero.CompiledModule
	modTime time.Time
	size    int64
}

// wasmCallKey carries a module run's state to the host functions.
type wasmCallKey struct{}

type wasmCall struct {
	plugin  string
	call    HiveCall
	pending []byte
}

// CallError is a daemon error a HiveCall returns, with its wire code.
type CallError struct {
	Code    string
	Message string
}

func (e *CallError) Error() string { return e.Message }

func (w *wasmHost) init(ctx context.Context) error {
	w.once.Do(func() {
		cfg := wazero.NewRuntimeConfig().WithMemoryLimitPages(wasmMemoryPages).WithCloseOnContextDone(true)
		w.ctx = context.WithoutCancel(ctx)
		w.rt = wazero.NewRuntimeWithConfig(ctx, cfg)
		w.modules = map[string]wasmCompiled{}
		if _, err := wasi_snapshot_preview1.Instantiate(ctx, w.rt); err != nil {
			w.initErr = err
			return
		}
		_, w.initErr = w.rt.NewHostModuleBuilder("hive").
			NewFunctionBuilder().WithFunc(wasmHiveCall).Export("call").
			NewFunctionBuilder().WithFunc(wasmHiveResponse).Export("response").
			Instantiate(ctx)
	})
	return w.initErr
}

// close frees the runtime and every compiled module.
func (w *wasmHost) close(ctx context.Context) {
	if w.rt != nil {
		_ = w.rt.Close(ctx)
	}
}

// compile returns path's compiled module, compiling it again when the file
// changed (a linked plugin under development).
func (w *wasmHost) compile(ctx context.Context, path string) (wazero.CompiledModule, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxWasmModuleBytes {
		return nil, fmt.Errorf("%s exceeds %d MiB", filepath.Base(path), MaxWasmModuleBytes>>20)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if c, ok := w.modules[path]; ok && c.modTime.Equal(info.ModTime()) && c.size == info.Size() {
		return c.mod, nil
	}
	code, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	mod, err := w.rt.CompileModule(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("compile %s: %w", filepath.Base(path), err)
	}
	if old, ok := w.modules[path]; ok {
		_ = old.mod.Close(ctx)
	}
	w.modules[path] = wasmCompiled{mod: mod, modTime: info.ModTime(), size: info.Size()}
	return mod, nil
}

// forget drops a plugin's compiled modules (it was unloaded).
func (w *wasmHost) forget(dir string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for path, c := range w.modules {
		if strings.HasPrefix(path, dir+string(filepath.Separator)) {
			_ = c.mod.Close(w.ctx)
			delete(w.modules, path)
		}
	}
}

// wasmModulePath resolves a command's module inside the plugin's package.
func wasmModulePath(dir, program string) (string, error) {
	rel := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(program, "./")))
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("wasm module %q must be in the plugin's package", program)
	}
	path := filepath.Join(dir, rel)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	if r, err := filepath.Rel(root, resolved); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("wasm module %q escapes the plugin's package", program)
	}
	return resolved, nil
}

// runWasm runs one command of a Wasm plugin to its end and returns its
// exit code.
func (h *Host) runWasm(ctx context.Context, l *loaded, argv []string, stdin []byte, vars map[string]string, stdout, stderr io.Writer, data string) (int, error) {
	if err := h.wasm.init(ctx); err != nil {
		return 0, fmt.Errorf("start the wasm runtime: %w", err)
	}
	path, err := wasmModulePath(l.Dir, argv[0])
	if err != nil {
		return 0, err
	}
	compiled, err := h.wasm.compile(ctx, path)
	if err != nil {
		return 0, err
	}
	cfg := wazero.NewModuleConfig().
		WithName("").
		WithArgs(append([]string{filepath.Base(path)}, argv[1:]...)...).
		WithStdin(bytes.NewReader(stdin)).
		WithStdout(stdout).WithStderr(stderr).
		WithFSConfig(wazero.NewFSConfig().WithDirMount(data, "/data").WithDirMount(vars["HIVE_PLUGIN_CONFIG_DIR"], "/config")).
		WithSysWalltime().WithSysNanotime().WithRandSource(rand.Reader)
	for _, k := range []string{"HIVE_PLUGIN_ID", "HIVE_PLUGIN_VERSION", "HIVE_PLUGIN_API", "HIVE_PLUGIN_CAPABILITIES", "HIVE_SESSION", "HIVE_PLUGIN_CONFIG_FROM_VERSION", "HIVE_PLUGIN_CONFIG_TO_VERSION"} {
		if v, ok := vars[k]; ok {
			cfg = cfg.WithEnv(k, v)
		}
	}
	// Its directories, as the module sees them.
	cfg = cfg.WithEnv("HIVE_PLUGIN_DATA", "/data").WithEnv("HIVE_PLUGIN_CONFIG_DIR", "/config")
	if c := vars["HIVE_PLUGIN_CONFIG"]; c != "" {
		cfg = cfg.WithEnv("HIVE_PLUGIN_CONFIG", "/config/"+filepath.Base(c))
	}
	if ctxFile, ok := vars["HIVE_PLUGIN_CONTEXT"]; ok {
		cfg = cfg.WithEnv("HIVE_PLUGIN_CONTEXT", "/data/"+filepath.Base(ctxFile))
	}
	state := &wasmCall{plugin: l.ID, call: h.cfg.Call}
	mod, err := h.wasm.rt.InstantiateModule(context.WithValue(ctx, wasmCallKey{}, state), compiled, cfg)
	if mod != nil {
		_ = mod.Close(context.WithoutCancel(ctx))
	}
	if exit, ok := errors.AsType[*sys.ExitError](err); ok {
		code := int(exit.ExitCode())
		if ctx.Err() != nil {
			return code, ctx.Err()
		}
		if code == 0 {
			return 0, nil
		}
		return code, fmt.Errorf("exit status %d", code)
	}
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, fmt.Errorf("wasm: %w", err)
	}
	return 0, nil
}

// wasmHiveCall is hive.call: it performs a request and keeps the response
// for hive.response.
func wasmHiveCall(ctx context.Context, m api.Module, ptr, size uint32) uint32 {
	state, _ := ctx.Value(wasmCallKey{}).(*wasmCall)
	if state == nil {
		return 0
	}
	respond := func(v any) uint32 {
		state.pending, _ = json.Marshal(v)
		return uint32(len(state.pending))
	}
	fail := func(code, msg string) uint32 {
		return respond(map[string]any{"error": map[string]string{"code": code, "message": msg}})
	}
	if size > maxWasmRequest {
		return fail("invalid_params", fmt.Sprintf("a request is at most %d bytes", maxWasmRequest))
	}
	raw, ok := m.Memory().Read(ptr, size)
	if !ok {
		return fail("invalid_params", "the request is outside the module's memory")
	}
	var req struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &req); err != nil || req.Method == "" {
		return fail("invalid_params", "a request is {\"method\": …, \"params\": …}")
	}
	if state.call == nil {
		return fail("unavailable", "this hive takes no calls from wasm plugins")
	}
	result, err := state.call(ctx, state.plugin, req.Method, req.Params)
	if err != nil {
		code := "internal_error"
		if ce, ok := errors.AsType[*CallError](err); ok {
			code = ce.Code
		}
		return fail(code, err.Error())
	}
	if len(result) > maxWasmResponse {
		return fail("internal_error", "the response is too large")
	}
	if len(result) == 0 {
		result = json.RawMessage("null")
	}
	return respond(map[string]json.RawMessage{"result": result})
}

// wasmHiveResponse is hive.response: it copies the last response into the
// guest's memory.
func wasmHiveResponse(ctx context.Context, m api.Module, ptr uint32) {
	state, _ := ctx.Value(wasmCallKey{}).(*wasmCall)
	if state == nil || state.pending == nil {
		return
	}
	m.Memory().Write(ptr, state.pending)
	state.pending = nil
}

// checkWasm checks a Wasm plugin: plugin API 2, every command a module in
// its package, and no isolation key (it needs none). A plugin that is not
// Wasm may not run modules, which need the runtime declared.
func (m *Manifest) checkWasm() error {
	wasm := 0
	for i, r := range m.Runtimes {
		if r.Kind == RuntimeWasm {
			wasm++
			if r.Command != "" || r.MinVersion != "" {
				return fmt.Errorf("runtime[%d]: a wasm runtime has no command or min_version", i)
			}
		}
	}
	commands := m.allCommands()
	if wasm == 0 {
		for _, c := range commands {
			if IsWasmCommand(c) {
				return fmt.Errorf("%s is a WebAssembly module: declare [[runtime]] kind = \"wasm\"", c[0])
			}
		}
		return nil
	}
	switch {
	case wasm > 1:
		return errors.New("runtime: one wasm runtime")
	case m.EffectiveAPI() < 2:
		return errors.New("[[runtime]] kind = \"wasm\" needs api = 2")
	case m.Isolation != "":
		return errors.New("a wasm plugin needs no isolation key: it runs confined everywhere")
	case len(m.Permissions.Network.Hosts)+len(m.Permissions.Process.Exec) > 0:
		return errors.New("a wasm plugin has no network and runs no programs: drop [permissions.network] and [permissions.process]")
	}
	for _, c := range commands {
		if !IsWasmCommand(c) {
			return fmt.Errorf("command %q: a wasm plugin runs only its .wasm modules", strings.Join(c, " "))
		}
		if p := c[0]; filepath.IsAbs(p) || strings.HasPrefix(filepath.Clean(strings.TrimPrefix(p, "./")), "..") {
			return fmt.Errorf("module %q must be in the plugin's package", p)
		}
	}
	return nil
}

// allCommands are every command the manifest runs.
func (m *Manifest) allCommands() [][]string {
	var out [][]string
	for _, s := range m.Startup {
		out = append(out, s.Command)
	}
	for _, a := range m.Actions {
		out = append(out, a.Command)
	}
	for _, e := range m.Events {
		out = append(out, e.Command)
	}
	for _, mg := range m.Migrations {
		out = append(out, mg.Command)
	}
	return out
}
