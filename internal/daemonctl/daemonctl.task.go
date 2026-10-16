package daemonctl

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"text/template"
	"unicode/utf16"
)

// TaskName names the Windows scheduled task that runs the daemon.
const TaskName = "Hive Daemon"

// ScheduledTaskXML renders a per-user Task Scheduler task that starts the
// daemon at logon, hidden, without a time limit, restarted when it fails
// (a clean `hive stop` exits 0 and is not), and never twice. schtasks
// reads task XML as UTF-16, so it is returned encoded that way, with a
// byte-order mark.
//
// A task cannot set environment variables: the daemon gets the user's
// logon environment (PATH included), which is what agents need anyway.
func ScheduledTaskXML(spec UnitSpec) []byte {
	var b bytes.Buffer
	if err := taskTmpl.Execute(&b, spec); err != nil {
		panic(fmt.Sprintf("daemonctl: task template: %v", err))
	}
	units := utf16.Encode([]rune(b.String()))
	out := make([]byte, 2+2*len(units))
	binary.LittleEndian.PutUint16(out, 0xFEFF)
	for i, u := range units {
		binary.LittleEndian.PutUint16(out[2+2*i:], u)
	}
	return out
}

var taskTmpl = template.Must(template.New("task").Funcs(template.FuncMap{"x": xmlEscape}).Parse(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Hive daemon: keeps coding agents running (hive service install)</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>true</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>5</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>{{x .Executable}}</Command>
      <Arguments>daemon</Arguments>
    </Exec>
  </Actions>
</Task>
`))
