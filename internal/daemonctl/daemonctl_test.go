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
	"time"

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
		"KillMode=mixed",
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
			if want := filepath.Join(home, tt.wantPath); path != want {
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

func TestSpawnWaitsForReadiness(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ready")
	// The "daemon" becomes ready after a moment and then keeps running.
	err := daemonctl.Spawn(context.Background(), daemonctl.SpawnOptions{
		Executable: "sh",
		Args:       []string{"-c", "sleep 0.2; touch " + marker + "; sleep 2"},
		Ready: func(context.Context) error {
			_, err := os.Stat(marker)
			return err
		},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSpawnReportsEarlyExitWithStderr(t *testing.T) {
	err := daemonctl.Spawn(context.Background(), daemonctl.SpawnOptions{
		Executable: "sh",
		Args:       []string{"-c", "echo 'bad config at line 3' >&2; exit 1"},
		StderrPath: filepath.Join(t.TempDir(), "daemon.stderr"),
		LogPath:    "/nonexistent/daemon.log",
		Ready:      func(context.Context) error { return errors.New("not up") },
		Timeout:    5 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "bad config at line 3") || !strings.Contains(err.Error(), "/nonexistent/daemon.log") {
		t.Fatalf("want the daemon's stderr and log path in the error, got %v", err)
	}
}

func TestSpawnSucceedsWhenAnotherDaemonWonTheRace(t *testing.T) {
	calls := 0
	err := daemonctl.Spawn(context.Background(), daemonctl.SpawnOptions{
		Executable: "sh",
		Args:       []string{"-c", "exit 1"}, // ours loses and exits at once
		Ready: func(context.Context) error {
			calls++
			if calls == 1 {
				return errors.New("not yet")
			}
			return nil // the other client's daemon is up
		},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("got %v, want success against the winning daemon", err)
	}
}

func TestSpawnTimesOut(t *testing.T) {
	err := daemonctl.Spawn(context.Background(), daemonctl.SpawnOptions{
		Executable: "sh",
		Args:       []string{"-c", "sleep 1"},
		Ready:      func(context.Context) error { return errors.New("never") },
		Timeout:    200 * time.Millisecond,
	})
	if !errors.Is(err, daemonctl.ErrStartTimeout) {
		t.Fatalf("got %v, want ErrStartTimeout", err)
	}
}

func TestSpawnedDaemonIsDetached(t *testing.T) {
	out := filepath.Join(t.TempDir(), "ids")
	ready := func(context.Context) error {
		data, err := os.ReadFile(out)
		if err != nil || !strings.Contains(string(data), "\n") {
			return errors.New("not yet")
		}
		return nil
	}
	// ps prints the child's session leader; with Setsid it is the child itself.
	err := daemonctl.Spawn(context.Background(), daemonctl.SpawnOptions{
		Executable: "sh",
		Args:       []string{"-c", `echo "$$ $(ps -o sess= -p $$ 2>/dev/null || ps -o sid= -p $$)" > ` + out + ".tmp; mv " + out + ".tmp " + out},
		Ready:      ready,
		Timeout:    5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(mustRead(t, out)))
	if len(fields) == 2 && fields[1] != "0" && fields[0] != fields[1] {
		t.Fatalf("daemon pid %s runs in session %s; want its own session", fields[0], fields[1])
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
