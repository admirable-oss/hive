package agent_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/vt"
)

var dump = flag.Bool("dump", false, "print every fixture checkpoint's screen")

// fixture is a recorded (or, for agents not available to record, written)
// terminal session: <name>.raw holds the bytes, <name>.json the size, the
// agent it shows and the checkpoints where its state is known.
type fixture struct {
	Name        string
	Agent       string       `json:"agent"`
	Argv        []string     `json:"argv"`
	Cols        int          `json:"cols"`
	Rows        int          `json:"rows"`
	Synthetic   bool         `json:"synthetic"`
	Checkpoints []checkpoint `json:"checkpoints"`
	raw         []byte
}

type checkpoint struct {
	Offset int         `json:"offset"`
	Label  string      `json:"label"`
	State  agent.State `json:"state"`
}

func loadFixtures(t *testing.T) []fixture {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "*", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out []fixture
	for _, f := range files {
		var fx fixture
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &fx); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		fx.Name = strings.TrimSuffix(f, ".json")
		if fx.raw, err = os.ReadFile(fx.Name + ".raw"); err != nil {
			t.Fatal(err)
		}
		out = append(out, fx)
	}
	return out
}

// screenAt replays a fixture's bytes up to offset.
func screenAt(t *testing.T, fx fixture, offset int) *vt.Screen {
	t.Helper()
	term := vt.New(fx.Cols, fx.Rows, vt.Options{})
	defer term.Close()
	if _, err := term.Write(fx.raw[:offset]); err != nil {
		t.Fatal(err)
	}
	return term.Snapshot()
}

func dumpScreen(name string, s *vt.Screen) {
	fmt.Printf("===== %s (title %q)\n", name, s.Title)
	for y := range s.Lines {
		if l := s.LineText(y); l != "" {
			fmt.Printf("%2d|%s\n", y, l)
		}
	}
}

func TestFixtureScreens(t *testing.T) {
	if !*dump {
		t.Skip("run with -dump to print the fixture screens")
	}
	for _, fx := range loadFixtures(t) {
		for _, cp := range fx.Checkpoints {
			dumpScreen(fmt.Sprintf("%s @%d %s (want %s)", fx.Name, cp.Offset, cp.Label, cp.State), screenAt(t, fx, cp.Offset))
		}
	}
}

// TestFixtureClassification replays every fixture and checks that its
// manifest reads each checkpoint's state, as the service would on a quiet
// screen: no rule matching means idle.
func TestFixtureClassification(t *testing.T) {
	ms, warnings, err := agent.LoadManifests("")
	if err != nil || len(warnings) > 0 {
		t.Fatalf("LoadManifests: %v %v", err, warnings)
	}
	type tally struct{ right, total int }
	scores := map[string]*tally{}
	for _, fx := range loadFixtures(t) {
		i := slices.IndexFunc(ms, func(m *agent.Manifest) bool { return m.ID == fx.Agent })
		if i < 0 {
			t.Errorf("%s: no manifest %q", fx.Name, fx.Agent)
			continue
		}
		m := ms[i]
		if fx.Synthetic && !m.Synthetic {
			t.Errorf("%s: a synthetic fixture for %s, whose manifest does not say synthetic = true", fx.Name, m.ID)
		}
		if len(fx.Argv) > 0 {
			if got := agent.Recognise(ms, fx.Argv); got != m {
				t.Errorf("%s: %q recognised as %v, want %s", fx.Name, fx.Argv, got, m.ID)
			}
		}
		sc := scores[m.ID]
		if sc == nil {
			sc = &tally{}
			scores[m.ID] = sc
		}
		for _, cp := range fx.Checkpoints {
			s := screenAt(t, fx, cp.Offset)
			v := m.Classify(s)
			got := v.State
			if got == agent.StateUnknown {
				got = agent.StateIdle
			}
			sc.total++
			if got == cp.State {
				sc.right++
				continue
			}
			t.Errorf("%s @%d %s: classified %s (rule %d %q matched %q), want %s",
				fx.Name, cp.Offset, cp.Label, got, v.Rule, v.Description, v.Text, cp.State)
			if testing.Verbose() {
				dumpScreen(cp.Label, s)
			}
		}
	}
	for _, id := range slices.Sorted(maps.Keys(scores)) {
		sc := scores[id]
		t.Logf("%-8s %d/%d checkpoints", id, sc.right, sc.total)
	}
}
