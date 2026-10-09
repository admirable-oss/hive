package daemonctl

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"
)

// Label identifies the launchd agent; Unit names the systemd user service.
const (
	Label = "io.github.admirable-oss.hive"
	Unit  = "hive.service"
)

// UnitSpec is what a service definition needs to run the daemon.
type UnitSpec struct {
	// Executable is the absolute path of the hive binary.
	Executable string
	// Env is passed to the daemon. PATH matters most: service managers start
	// with a minimal PATH, but agents need the user's tools (node, git, …).
	Env map[string]string
	// LogDir receives the service manager's capture of stdout/stderr. The
	// daemon writes its own structured log there as well.
	LogDir string
}

// LaunchdPlist renders a per-user launchd agent. KeepAlive restarts the
// daemon after a crash but not after a clean `hive stop`. AbandonProcessGroup
// keeps launchd from killing leftover processes when the daemon exits (shims
// run in their own sessions anyway; this makes it explicit).
func LaunchdPlist(spec UnitSpec) []byte {
	var b bytes.Buffer
	if err := plistTmpl.Execute(&b, spec); err != nil {
		panic(fmt.Sprintf("daemonctl: plist template: %v", err))
	}
	return b.Bytes()
}

// SystemdUnit renders a systemd user service. KillMode=process signals only
// the daemon: agents run under shims in the same cgroup, and stopping or
// restarting the service must leave them running for the next daemon.
func SystemdUnit(spec UnitSpec) []byte {
	var b bytes.Buffer
	if err := systemdTmpl.Execute(&b, spec); err != nil {
		panic(fmt.Sprintf("daemonctl: unit template: %v", err))
	}
	return b.Bytes()
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// systemdQuote quotes a value for an Environment= or ExecStart= line.
func systemdQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$")
	return `"` + r.Replace(s) + `"`
}

var funcs = template.FuncMap{
	"xml":  xmlEscape,
	"sq":   systemdQuote,
	"join": filepath.Join,
}

var plistTmpl = template.Must(template.New("plist").Funcs(funcs).Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + Label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{xml .Executable}}</string>
		<string>daemon</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
{{- range $k, $v := .Env}}
		<key>{{xml $k}}</key>
		<string>{{xml $v}}</string>
{{- end}}
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>AbandonProcessGroup</key>
	<true/>
	<key>ExitTimeOut</key>
	<integer>30</integer>
	<key>StandardOutPath</key>
	<string>{{xml (join .LogDir "launchd.log")}}</string>
	<key>StandardErrorPath</key>
	<string>{{xml (join .LogDir "launchd.log")}}</string>
</dict>
</plist>
`))

var systemdTmpl = template.Must(template.New("unit").Funcs(funcs).Parse(`[Unit]
Description=Hive agent runtime
Documentation=https://github.com/admirable-oss/hive

[Service]
Type=simple
ExecStart={{sq .Executable}} daemon
{{- range $k, $v := .Env}}
Environment={{sq (printf "%s=%s" $k $v)}}
{{- end}}
Restart=on-failure
RestartSec=2
KillMode=process
TimeoutStopSec=30

[Install]
WantedBy=default.target
`))
