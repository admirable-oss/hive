package plugin

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
)

// isolate decides how a plugin runs, and says why it cannot run at all.
//
// A plugin API 1 plugin runs sandboxed when it declares isolation =
// "strict" (auto) or the user asks for every plugin to be ([plugins]
// isolation = "strict"). A plugin API 2 plugin runs sandboxed and
// confined wherever a sandbox exists, whatever [plugins] isolation says
// short of strict (a setting never silently loosens API 2); it runs
// outside the sandbox only with unconfined approved, for isolation =
// "none" or a system without a sandbox.
func (h *Host) isolate(l *loaded, m *Manifest) string {
	cfg := h.cfg.Isolation
	if m.EffectiveAPI() < 2 {
		if cfg == IsolationAPI2 {
			return `[plugins] isolation = "api2" runs only plugin API 2 plugins, and this one is API 1`
		}
		l.strict = cfg == IsolationStrict || (cfg == IsolationAuto && m.Isolation == "strict" && SandboxAvailable())
		if l.strict && !SandboxAvailable() {
			return "[plugins] isolation is strict, which needs " + sandboxTool() + ", not available here"
		}
		return ""
	}
	unconfined := slices.Contains(l.grant.Effective, CapUnconfined)
	switch {
	case cfg == IsolationStrict && !SandboxAvailable():
		return "[plugins] isolation is strict, which needs " + sandboxTool() + ", not available here"
	case cfg == IsolationStrict:
		l.strict, l.confined = true, true
	case m.Isolation == IsolationNone && unconfined:
		l.health.Warnings = append(l.health.Warnings, "runs unconfined, outside the sandbox, as you approved: "+m.IsolationReason)
	case SandboxAvailable():
		l.strict, l.confined = true, true
	case unconfined:
		l.health.Warnings = append(l.health.Warnings, "this system has no sandbox for plugins ("+sandboxTool()+"), so it runs unconfined, as you approved")
	default:
		return fmt.Sprintf("this system has no sandbox for plugins (%s); run it unconfined only if you trust it: `hive plugin grant %s %s`", sandboxTool(), l.ID, CapUnconfined)
	}
	return ""
}

// confine resolves what a confined plugin may run: its own package, the
// hive command, its runtimes, and the programs it lists (when it holds
// process.exec), found on PATH as it loads.
func (h *Host) confine(l *loaded, m *Manifest) (warnings []string) {
	l.exec = nil
	add := func(p string) {
		if p == "" {
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		l.exec = append(l.exec, p)
		if resolved, err := filepath.EvalSymlinks(p); err == nil && resolved != p {
			l.exec = append(l.exec, resolved)
		}
	}
	add(l.Dir)
	add(h.cfg.Launcher)
	for _, r := range m.Runtimes {
		if p, err := exec.LookPath(r.Command); err == nil {
			add(p)
		}
	}
	if slices.Contains(l.grant.Effective, CapProcessExec) {
		for _, prog := range m.Permissions.Process.Exec {
			p, err := exec.LookPath(prog)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s is not on PATH, so it cannot run", prog))
				continue
			}
			add(p)
		}
	}
	slices.Sort(l.exec)
	l.exec = slices.Compact(l.exec)
	return warnings
}

// sandboxFor is the sandbox of one of a confined plugin's commands.
func (h *Host) sandboxFor(ctx context.Context, l *loaded, caps []string, data, config, cache string) sandboxSpec {
	has := func(c string) bool { return slices.Contains(caps, c) }
	s := sandboxSpec{
		Writable:        []string{data, config, cache},
		Network:         has(CapNetwork),
		FilesystemWrite: has(CapFilesystemWrite),
		Socket:          l.socket,
		DaemonSocket:    h.cfg.SocketPath,
	}
	if !l.confined {
		return s
	}
	m := l.Manifest
	s.Confined, s.Launcher = true, h.cfg.Launcher
	s.ReadAll = has(CapFilesystemRead)
	s.Readable = []string{l.Dir}
	if has(CapFilesystemReadWorkspace) || has(CapFilesystemWriteWorkspace) {
		if h.cfg.Workspaces != nil {
			ws := h.cfg.Workspaces(ctx)
			s.Readable = append(s.Readable, ws...)
			if has(CapFilesystemWriteWorkspace) {
				s.Writable = append(s.Writable, ws...)
			}
		}
	}
	s.Exec = slices.Clone(l.exec)
	s.ExecAny = has(CapProcessExec) && len(m.Permissions.Process.Exec) == 0
	if l.proxy != nil {
		s.Network = false
		s.ProxyPort, s.ProxySocket, s.ProxyToken = l.proxy.Port(), l.proxy.Socket(), l.proxy.token
	}
	return s
}
