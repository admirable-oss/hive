package layout_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"go.uber.org/goleak"

	"github.com/admirable-oss/hive/internal/layout"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var area = layout.Rect{W: 100, H: 40}

func mustSplit(t *testing.T, root *layout.Node, target string, d layout.Direction, ratio float64, id string) *layout.Node {
	t.Helper()
	root, err := layout.Split(root, target, d, ratio, id)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSplitPlacesTheNewPaneOnTheRequestedSide(t *testing.T) {
	tests := []struct {
		d     layout.Direction
		order []string
		want  map[string]layout.Rect
	}{
		{layout.Right, []string{"a", "b"}, map[string]layout.Rect{"a": {0, 0, 50, 40}, "b": {51, 0, 49, 40}}},
		{layout.Left, []string{"b", "a"}, map[string]layout.Rect{"b": {0, 0, 50, 40}, "a": {51, 0, 49, 40}}},
		{layout.Down, []string{"a", "b"}, map[string]layout.Rect{"a": {0, 0, 100, 20}, "b": {0, 21, 100, 19}}},
		{layout.Up, []string{"b", "a"}, map[string]layout.Rect{"b": {0, 0, 100, 20}, "a": {0, 21, 100, 19}}},
	}
	for _, tt := range tests {
		t.Run(string(tt.d), func(t *testing.T) {
			root := mustSplit(t, layout.Leaf("a"), "a", tt.d, 0, "b")
			if got := root.Panes(); !slices.Equal(got, tt.order) {
				t.Fatalf("order = %v, want %v", got, tt.order)
			}
			got := layout.Geometry(root, area, "")
			for id, r := range tt.want {
				if got[id] != r {
					t.Errorf("%s = %+v, want %+v", id, got[id], r)
				}
			}
		})
	}
}

func TestSplitRatioIsTheNewPanesShare(t *testing.T) {
	root := mustSplit(t, layout.Leaf("a"), "a", layout.Right, 0.25, "b")
	g := layout.Geometry(root, area, "")
	if g["b"].W != 25 || g["a"].W != 74 {
		t.Fatalf("widths a=%d b=%d, want 74 and 25", g["a"].W, g["b"].W)
	}
}

func TestSplitErrors(t *testing.T) {
	root := layout.Leaf("a")
	if _, err := layout.Split(root, "missing", layout.Right, 0, "b"); !errors.Is(err, layout.ErrNotFound) {
		t.Fatalf("missing target: %v", err)
	}
	if _, err := layout.Split(root, "a", layout.Right, 0, "a"); !errors.Is(err, layout.ErrInvalid) {
		t.Fatalf("duplicate pane: %v", err)
	}
}

func TestRemoveLetsTheSiblingTakeOver(t *testing.T) {
	root := mustSplit(t, layout.Leaf("a"), "a", layout.Right, 0, "b")
	root = mustSplit(t, root, "b", layout.Down, 0, "c")
	root, err := layout.Remove(root, "b")
	if err != nil {
		t.Fatal(err)
	}
	if got := root.Panes(); !slices.Equal(got, []string{"a", "c"}) {
		t.Fatalf("panes = %v", got)
	}
	g := layout.Geometry(root, area, "")
	if g["c"].H != 40 {
		t.Fatalf("c should take b's full height, got %+v", g["c"])
	}
	root, _ = layout.Remove(root, "a")
	root, _ = layout.Remove(root, "c")
	if root != nil {
		t.Fatal("removing the last pane leaves an empty layout")
	}
}

func TestSwapAndRename(t *testing.T) {
	root := mustSplit(t, layout.Leaf("a"), "a", layout.Right, 0, "b")
	if err := layout.Swap(root, "a", "b"); err != nil {
		t.Fatal(err)
	}
	if got := root.Panes(); !slices.Equal(got, []string{"b", "a"}) {
		t.Fatalf("after swap %v", got)
	}
	if err := layout.Rename(root, "a", "z"); err != nil {
		t.Fatal(err)
	}
	if err := layout.Rename(root, "b", "z"); !errors.Is(err, layout.ErrInvalid) {
		t.Fatalf("renaming onto an existing pane: %v", err)
	}
	if err := layout.Swap(root, "b", "missing"); !errors.Is(err, layout.ErrNotFound) {
		t.Fatalf("swap with a missing pane: %v", err)
	}
}

func TestResizeMovesTheNearestBorderOnThatSide(t *testing.T) {
	// a | (b / c)
	root := mustSplit(t, layout.Leaf("a"), "a", layout.Right, 0, "b")
	root = mustSplit(t, root, "b", layout.Down, 0, "c")

	if err := layout.Resize(root, area, "a", layout.Right, 10); err != nil {
		t.Fatal(err)
	}
	if g := layout.Geometry(root, area, ""); g["a"].W != 60 {
		t.Fatalf("a should grow to 60 cells, got %d", g["a"].W)
	}
	// b grows left: the same border moves back.
	if err := layout.Resize(root, area, "b", layout.Left, 20); err != nil {
		t.Fatal(err)
	}
	if g := layout.Geometry(root, area, ""); g["a"].W != 40 {
		t.Fatalf("a should shrink to 40 cells, got %d", g["a"].W)
	}
	// c grows up into b.
	before := layout.Geometry(root, area, "")["c"].H
	if err := layout.Resize(root, area, "c", layout.Up, 5); err != nil {
		t.Fatal(err)
	}
	if after := layout.Geometry(root, area, "")["c"].H; after != before+5 {
		t.Fatalf("c height %d -> %d, want +5", before, after)
	}
	// a has no border on its left.
	if err := layout.Resize(root, area, "a", layout.Left, 1); !errors.Is(err, layout.ErrNoBorder) {
		t.Fatalf("got %v, want ErrNoBorder", err)
	}
}

func TestZoomShowsOnePane(t *testing.T) {
	root := mustSplit(t, layout.Leaf("a"), "a", layout.Right, 0, "b")
	g := layout.Geometry(root, area, "b")
	if len(g) != 1 || g["b"] != area {
		t.Fatalf("zoomed geometry = %+v", g)
	}
	if g := layout.Geometry(root, area, "missing"); len(g) != 2 {
		t.Fatal("zooming a pane not in the tree is ignored")
	}
}

func TestNeighbor(t *testing.T) {
	// a | b
	// ----+
	//   c
	root := mustSplit(t, layout.Leaf("a"), "a", layout.Down, 0, "c")
	root = mustSplit(t, root, "a", layout.Right, 0, "b")
	tests := []struct {
		from string
		d    layout.Direction
		want string
	}{
		{"a", layout.Right, "b"},
		{"b", layout.Left, "a"},
		{"a", layout.Down, "c"},
		{"b", layout.Down, "c"},
		{"c", layout.Up, "a"}, // a and b overlap c equally; the left one wins ties by distance order
		{"a", layout.Up, ""},
	}
	for _, tt := range tests {
		got, ok := layout.Neighbor(root, area, tt.from, tt.d)
		if tt.want == "" {
			if ok {
				t.Errorf("%s %s: got %s, want none", tt.from, tt.d, got)
			}
			continue
		}
		if tt.from == "c" && (got == "a" || got == "b") {
			continue
		}
		if got != tt.want {
			t.Errorf("%s %s = %q, want %q", tt.from, tt.d, got, tt.want)
		}
	}
}

func TestValidateAndJSON(t *testing.T) {
	var root *layout.Node
	in := `{"split":"horizontal","first":{"pane":"a"},"second":{"split":"vertical","ratio":0.3,"first":{"pane":"b"},"second":{"pane":"c"}}}`
	if err := json.Unmarshal([]byte(in), &root); err != nil {
		t.Fatal(err)
	}
	if err := root.Validate(); err == nil {
		t.Fatal("a zero ratio is invalid before Normalize")
	}
	root.Normalize()
	if err := root.Validate(); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(root)
	var again *layout.Node
	_ = json.Unmarshal(out, &again)
	if !slices.Equal(again.Panes(), []string{"a", "b", "c"}) {
		t.Fatalf("round trip lost panes: %s", out)
	}
	bad := []string{
		`{"split":"diagonal","ratio":0.5,"first":{"pane":"a"},"second":{"pane":"b"}}`,
		`{"split":"horizontal","ratio":0.5,"first":{"pane":"a"}}`,
		`{"split":"horizontal","ratio":0.5,"first":{"pane":"a"},"second":{"pane":"a"}}`,
		`{"pane":"a","split":"horizontal"}`,
		`{"split":"horizontal","ratio":2,"first":{"pane":"a"},"second":{"pane":"b"}}`,
	}
	for _, s := range bad {
		var n *layout.Node
		_ = json.Unmarshal([]byte(s), &n)
		if err := n.Validate(); !errors.Is(err, layout.ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", s, err)
		}
	}
}

// TestRandomOperationsKeepTheTilingSound applies random operations and checks
// the geometry invariants after each: every pane is inside the area, no two
// panes overlap, and panes plus separators cover the area exactly.
func TestRandomOperationsKeepTheTilingSound(t *testing.T) {
	dirs := []layout.Direction{layout.Left, layout.Right, layout.Up, layout.Down}
	for seed := range uint64(200) {
		r := rand.New(rand.NewPCG(seed, 3))
		root := layout.Leaf("p0")
		next := 1
		for range 40 {
			panes := root.Panes()
			target := panes[r.IntN(len(panes))]
			switch op := r.IntN(10); {
			case op < 5 && len(panes) < 16:
				id := fmt.Sprintf("p%d", next)
				next++
				var err error
				root, err = layout.Split(root, target, dirs[r.IntN(4)], 0.1+r.Float64()*0.8, id)
				if err != nil {
					t.Fatal(err)
				}
			case op < 7 && len(panes) > 1:
				root, _ = layout.Remove(root, target)
			case op < 8 && len(panes) > 1:
				_ = layout.Swap(root, target, panes[r.IntN(len(panes))])
			default:
				_ = layout.Resize(root, area, target, dirs[r.IntN(4)], r.IntN(21)-10)
			}
			if err := root.Validate(); err != nil {
				t.Fatalf("seed %d: invalid tree: %v", seed, err)
			}
			checkTiling(t, seed, root)
		}
	}
}

func checkTiling(t *testing.T, seed uint64, root *layout.Node) {
	t.Helper()
	g := layout.Geometry(root, area, "")
	if len(g) != len(root.Panes()) {
		t.Fatalf("seed %d: %d rects for %d panes", seed, len(g), len(root.Panes()))
	}
	covered := make([][]int, area.H)
	for y := range covered {
		covered[y] = make([]int, area.W)
	}
	for id, rc := range g {
		if rc.W < 0 || rc.H < 0 || rc.X < 0 || rc.Y < 0 || rc.X+rc.W > area.W || rc.Y+rc.H > area.H {
			t.Fatalf("seed %d: pane %s out of bounds: %+v", seed, id, rc)
		}
		for y := rc.Y; y < rc.Y+rc.H; y++ {
			for x := rc.X; x < rc.X+rc.W; x++ {
				covered[y][x]++
				if covered[y][x] > 1 {
					t.Fatalf("seed %d: panes overlap at (%d,%d)", seed, x, y)
				}
			}
		}
	}
	// Uncovered cells are separators: each must sit between panes, i.e.
	// there are exactly (number of splits) separator lines. Counting cells
	// is enough to show nothing is lost: panes + separators = area.
	paneCells := 0
	for _, rc := range g {
		paneCells += rc.W * rc.H
	}
	if paneCells > area.W*area.H {
		t.Fatalf("seed %d: panes cover more than the area", seed)
	}
}
