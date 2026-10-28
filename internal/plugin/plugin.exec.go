package plugin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/pgroup"
)

// CacheDir is plugin id's disposable cache: removed on uninstall, and safe
// for the user to delete at any time.
func (s *Store) CacheDir(id string) string { return filepath.Join(s.dir, ".cache", id) }

// run runs one plugin command to its end and returns its exit code. Its
// output goes to the plugin's log (and to also, when set).
func (h *Host) run(ctx context.Context, l *loaded, argv []string, c Context, also io.Writer) (int, error) {
	return h.runWithEnvironment(ctx, l, argv, c, also, nil)
}

// pluginDirs makes and returns a plugin's data, configuration and cache
// directories.
func (h *Host) pluginDirs(l *loaded) (data, config, cache string, err error) {
	data = h.cfg.Store.DataDir(l.ID)
	if l.Manifest.DataDirectory != "" && l.Manifest.DataDirectory != "." {
		data = filepath.Join(data, filepath.FromSlash(l.Manifest.DataDirectory))
	}
	config = h.cfg.Store.ConfigDir(l.ID)
	if l.Manifest.Configuration.Directory != "" && l.Manifest.Configuration.Directory != "." {
		config = filepath.Join(config, filepath.FromSlash(l.Manifest.Configuration.Directory))
	}
	cache = h.cfg.Store.CacheDir(l.ID)
	for _, d := range []string{data, config, filepath.Join(cache, "tmp")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", "", "", err
		}
	}
	return data, config, cache, nil
}

func (h *Host) runWithEnvironment(ctx context.Context, l *loaded, argv []string, c Context, also io.Writer, extra map[string]string) (int, error) {
	c.API, c.Plugin, c.Session = APIVersion, l.ID, h.cfg.Session
	if l.Manifest != nil {
		c.API = l.Manifest.EffectiveAPI()
	}
	h.mu.Lock()
	c.Capabilities = slices.Clone(l.grant.Effective)
	h.mu.Unlock()
	if c.Capabilities == nil {
		c.Capabilities = []string{}
	}
	data, configDir, cache, err := h.pluginDirs(l)
	if err != nil {
		return 0, err
	}
	ctxJSON, _ := json.Marshal(c)
	ctxFile := filepath.Join(data, ".context-"+randomID()+".json")
	if err := os.WriteFile(ctxFile, ctxJSON, 0o600); err != nil {
		return 0, err
	}
	defer os.Remove(ctxFile)

	path := argv[0]
	if strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") {
		path = filepath.Join(l.Dir, path)
	}
	vars := map[string]string{
		"HIVE_PLUGIN_ID":           l.ID,
		"HIVE_PLUGIN_DIR":          l.Dir,
		"HIVE_PLUGIN_DATA":         data,
		"HIVE_PLUGIN_CACHE":        cache,
		"HIVE_PLUGIN_CONFIG_DIR":   configDir,
		"HIVE_PLUGIN_CONFIG":       filepath.Join(configDir, pluginConfigFile),
		"HIVE_PLUGIN_VERSION":      l.Manifest.Version,
		"HIVE_PLUGIN_CONTEXT":      ctxFile,
		"HIVE_PLUGIN_API":          strconv.Itoa(c.API),
		"HIVE_PLUGIN_CAPABILITIES": strings.Join(c.Capabilities, ","),
	}
	if l.socket != "" {
		// SDKs read HIVE_SOCKET_PATH; the hive command, HIVE_PLUGIN_SOCKET.
		vars["HIVE_SOCKET_PATH"] = l.socket
		vars["HIVE_PLUGIN_SOCKET"] = l.socket
	}
	for key, value := range extra {
		vars[key] = value
	}
	env := h.cfg.Env(vars)
	full := append([]string{path}, argv[1:]...)
	if l.strict {
		env = strictEnv(env, cache)
		spec := h.sandboxFor(ctx, l, c.Capabilities, data, configDir, cache)
		if spec.ProxyPort != 0 {
			env = append(env, ProxyEnv(l.proxy.URL(fmt.Sprintf("127.0.0.1:%d", spec.ProxyPort)))...)
		}
		full, err = sandboxArgv(full, spec)
		if err != nil {
			return 0, err
		}
	}
	cmd := exec.CommandContext(ctx, full[0], full[1:]...)
	cmd.Dir = l.Dir
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(ctxJSON)
	var w io.Writer = pluginLog{l}
	if also != nil {
		w = io.MultiWriter(w, also)
	}
	cmd.Stdout, cmd.Stderr = w, w
	// Its own group, so stopping it stops what it started.
	pgroup.Configure(cmd)
	cmd.Cancel = func() error { return pgroup.TerminateAfter(cmd.Process.Pid, 3*time.Second) }
	cmd.WaitDelay = 3 * time.Second
	h.running.Add(1)
	defer h.running.Done()
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	if err := pgroup.Started(cmd); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return 0, err
	}
	err = cmd.Wait()
	_ = pgroup.Kill(cmd.Process.Pid) // anything it left running
	pgroup.Release(cmd.Process.Pid)
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode(), err
	}
	return 0, err
}

// strictKeep are the inherited variables a strictly isolated plugin sees,
// besides Hive's: no tokens or credentials from the user's environment.
var strictKeep = []string{"PATH", "LANG", "LANGUAGE", "TERM", "USER", "LOGNAME", "TZ"}

// strictEnv narrows env for a strict plugin: Hive's variables and a few
// harmless ones, with HOME and TMPDIR in its cache.
func strictEnv(env []string, cache string) []string {
	var out []string
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "HIVE_") || strings.HasPrefix(k, "LC_") || slices.Contains(strictKeep, k) {
			out = append(out, kv)
		}
	}
	return append(out, "HOME="+cache, "TMPDIR="+filepath.Join(cache, "tmp"))
}

func randomID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// pluginLog writes a command's output to its plugin's log, one write at a
// time. It never fails: an unloaded plugin's log is gone, and its commands
// are being stopped anyway.
type pluginLog struct{ l *loaded }

func (w pluginLog) Write(p []byte) (int, error) {
	w.l.logMu.Lock()
	defer w.l.logMu.Unlock()
	_, _ = w.l.log.Write(p)
	return len(p), nil
}

// tailBuffer keeps the last 64 KiB written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const tailKeep = 64 << 10

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailKeep {
		t.buf = t.buf[len(t.buf)-tailKeep:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
