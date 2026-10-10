package agent_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
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
