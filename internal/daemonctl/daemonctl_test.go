package daemonctl_test

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"go.uber.org/goleak"

	"github.com/admirable-oss/hive/internal/daemonctl"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var spec = daemonctl.UnitSpec{
	Executable: "/opt/hive tools/hive",
	Env:        map[string]string{"PATH": "/usr/bin:/opt/node & co/bin", "HIVE_HOME": "/Users/me/.hive"},
	LogDir:     "/Users/me/.hive/logs",
}

func TestLaunchdPlistIsValidXMLAndEscaped(t *testing.T) {
	plist := daemonctl.LaunchdPlist(spec)
	dec := xml.NewDecoder(strings.NewReader(string(plist)))
	dec.Strict = true
	dec.Entity = xml.HTMLEntity
	for {
		_, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("plist is not valid XML: %v\n%s", err, plist)
		}
	}
	for _, want := range []string{
		"<string>" + daemonctl.Label + "</string>",
		"<string>/opt/hive tools/hive</string>",
		"<string>daemon</string>",
		"/opt/node &amp; co/bin",
		"<key>HIVE_HOME</key>",
		"<key>SuccessfulExit</key>",
		"<key>AbandonProcessGroup</key>",
		"/Users/me/.hive/logs/launchd.log",
	} {
		if !strings.Contains(string(plist), want) {
			t.Errorf("plist missing %q", want)
		}
	}
}

func TestSystemdUnitQuotesValues(t *testing.T) {
	unit := string(daemonctl.SystemdUnit(daemonctl.UnitSpec{
		Executable: "/opt/hive tools/hive",
		Env:        map[string]string{"PATH": `/a:/b$x`, "PCT": "100%"},
		LogDir:     "/l",
	}))
	for _, want := range []string{
		`ExecStart="/opt/hive tools/hive" daemon`,
		`Environment="PATH=/a:/b$$x"`,
		`Environment="PCT=100%%"`,
		"Restart=on-failure",
		"KillMode=process",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q:\n%s", want, unit)
		}
	}
}

type recorder struct{ calls []string }

func (r *recorder) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return nil, nil
}

func TestManagerInstallAndUninstall(t *testing.T) {
	tests := []struct {
		goos          string
		wantPath      string
		wantInstall   []string
		wantUninstall []string
	}{
		{
			goos:     "darwin",
			wantPath: "Library/LaunchAgents/" + daemonctl.Label + ".plist",
			wantInstall: []string{
				"launchctl bootout gui/501/" + daemonctl.Label,
				"launchctl bootstrap gui/501 {path}",
			},
			wantUninstall: []string{"launchctl bootout gui/501/" + daemonctl.Label},
		},
		{
			goos:          "linux",
			wantPath:      ".config/systemd/user/hive.service",
			wantInstall:   []string{"systemctl --user daemon-reload", "systemctl --user enable --now hive.service"},
			wantUninstall: []string{"systemctl --user disable --now hive.service", "systemctl --user daemon-reload"},
		},
		{
			goos:     "windows",
			wantPath: "AppData/Local/Hive/daemon-task.xml",
			wantInstall: []string{
				"schtasks /Create /TN " + daemonctl.TaskName + " /XML {path} /F",
				"schtasks /Run /TN " + daemonctl.TaskName,
			},
			wantUninstall: []string{"schtasks /End /TN " + daemonctl.TaskName, "schtasks /Delete /TN " + daemonctl.TaskName + " /F"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			home := t.TempDir()
			rec := &recorder{}
			m := daemonctl.Manager{GOOS: tt.goos, Home: home, UID: 501, Cmd: rec}
			s := spec
			s.LogDir = filepath.Join(home, "logs")

			path, err := m.Install(context.Background(), s)
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(home, filepath.FromSlash(tt.wantPath)); path != want {
				t.Fatalf("path %q, want %q", path, want)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("definition not written: %v", err)
			}
			if _, err := os.Stat(s.LogDir); err != nil {
				t.Fatalf("log dir not created: %v", err)
			}
			want := make([]string, len(tt.wantInstall))
			for i, c := range tt.wantInstall {
				want[i] = strings.ReplaceAll(c, "{path}", path)
			}
			if !slices.Equal(rec.calls, want) {
				t.Fatalf("install ran %q, want %q", rec.calls, want)
			}

			rec.calls = nil
			if _, err := m.Uninstall(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("definition should be removed: %v", err)
			}
			if !slices.Equal(rec.calls, tt.wantUninstall) {
				t.Fatalf("uninstall ran %q, want %q", rec.calls, tt.wantUninstall)
			}
			if _, err := m.Uninstall(context.Background()); err != nil {
				t.Fatalf("second uninstall should be a no-op: %v", err)
			}
		})
	}
}

func TestManagerRejectsUnsupportedOS(t *testing.T) {
	m := daemonctl.Manager{GOOS: "plan9", Home: t.TempDir(), Cmd: &recorder{}}
	if _, err := m.Install(context.Background(), spec); !errors.Is(err, daemonctl.ErrUnsupportedOS) {
		t.Fatalf("got %v, want ErrUnsupportedOS", err)
	}
}

func TestScheduledTaskXML(t *testing.T) {
	raw := daemonctl.ScheduledTaskXML(daemonctl.UnitSpec{Executable: `C:\Users\me & co\hive.exe`})
	if len(raw) < 2 || raw[0] != 0xFF || raw[1] != 0xFE || len(raw)%2 != 0 {
		t.Fatal("task XML must be UTF-16LE with a byte-order mark")
	}
	units := make([]uint16, (len(raw)-2)/2)
	for i := range units {
		units[i] = uint16(raw[2+2*i]) | uint16(raw[3+2*i])<<8
	}
	text := string(utf16.Decode(units))
	for _, want := range []string{`<Command>C:\Users\me &amp; co\hive.exe</Command>`, "<Arguments>daemon</Arguments>", "<LogonTrigger>", "<MultipleInstancesPolicy>IgnoreNew", "<ExecutionTimeLimit>PT0S", "<RestartOnFailure>"} {
		if !strings.Contains(text, want) {
			t.Errorf("task XML lacks %q:\n%s", want, text)
		}
	}
}
