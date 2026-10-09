package vt_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/vt"
)

var update = flag.Bool("update", false, "rewrite the golden screens in testdata/recorded")

// TestRecordedSessions replays real programs (vim, less, a colour script),
// recorded in an 80x24 PTY by docs/adr/spikes/vt (`go run . dump`), and
// compares the final screen with a reviewed golden file. Run with -update
// after an intentional change in emulation, and review the diff.
func TestRecordedSessions(t *testing.T) {
	raws, err := filepath.Glob(filepath.Join("testdata", "recorded", "*.raw"))
	if err != nil || len(raws) == 0 {
		t.Fatalf("no recordings found: %v", err)
	}
	for _, raw := range raws {
		name := strings.TrimSuffix(filepath.Base(raw), ".raw")
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(raw)
			if err != nil {
				t.Fatal(err)
			}
			replay := func(chunk int) *vt.Terminal {
				term := newTerm(t, 80, 24, vt.Options{})
				for p := data; len(p) > 0; {
					n := min(chunk, len(p))
					_, _ = term.Write(p[:n])
					p = p[n:]
				}
				return term
			}
			term := replay(4096) // how a PTY reader delivers output
			got := term.Snapshot()

			golden := strings.TrimSuffix(raw, ".raw") + ".golden"
			if *update {
				if err := os.WriteFile(golden, []byte(got.Text()+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden file (run with -update): %v", err)
			}
			if got.Text()+"\n" != string(want) {
				t.Fatalf("screen differs from %s:\n%s", golden, got.Text())
			}

			// Unlucky read boundaries must not change the result (except
			// for grapheme clusters split across writes, which these
			// recordings do not contain).
			if odd := replay(7).Snapshot(); odd.Text() != got.Text() {
				t.Fatalf("7-byte chunks give a different screen:\n%s", odd.Text())
			}

			// Frames and the painter reproduce the recorded screen exactly.
			client := &vt.Screen{}
			var v vt.View
			client.Apply(term.Frame(&v))
			assertSameScreen(t, client, got)
			outer := newTerm(t, 80, 24, vt.Options{})
			var pv vt.View
			if err := vt.NewPainter(outer).Paint(term.Frame(&pv)); err != nil {
				t.Fatal(err)
			}
			if outer.Snapshot().Text() != got.Text() {
				t.Fatalf("painted screen differs:\n%s", outer.Snapshot().Text())
			}
		})
	}
}
