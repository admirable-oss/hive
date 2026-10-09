package fuzzy_test

import (
	"slices"
	"testing"

	"github.com/admirable-oss/hive/internal/tui/fuzzy"
)

func TestMatch(t *testing.T) {
	tests := []struct {
		pattern, text string
		ok            bool
		positions     []int
	}{
		{"", "anything", true, nil},
		{"cla", "claude", true, []int{0, 1, 2}},
		{"cl a", "claude", true, []int{0, 1, 2}},
		{"cde", "claude", true, []int{0, 4, 5}},
		{"xyz", "claude", false, nil},
		{"CL", "claude", false, nil},        // a capital makes it case-sensitive
		{"cl", "Claude", true, []int{0, 1}}, // lower case matches either
		{"ab", "a_xab", true, []int{3, 4}},  // tightened to the closest run
	}
	for _, tt := range tests {
		_, pos, ok := fuzzy.Match(tt.pattern, tt.text)
		if ok != tt.ok || !slices.Equal(pos, tt.positions) {
			t.Errorf("Match(%q, %q) = %v %v, want %v %v", tt.pattern, tt.text, pos, ok, tt.positions, tt.ok)
		}
	}
}

func TestRankPrefersWordStartsAndRuns(t *testing.T) {
	candidates := []string{
		"logs  web  tail -f",   // l…o…g scattered
		"codex  api  review",   // no match for "cl"
		"claude  api  running", // prefix run
		"tools  cli  lint",     // "cl" starts a word, later
	}
	got := fuzzy.Rank("cl", candidates)
	var order []int
	for _, r := range got {
		order = append(order, r.Index)
	}
	if !slices.Equal(order, []int{2, 3}) {
		t.Fatalf("rank order = %v, want [2 3]", order)
	}
	if all := fuzzy.Rank("", candidates); len(all) != 4 || all[0].Index != 0 {
		t.Fatal("an empty pattern keeps every candidate in order")
	}
	// camelCase boundaries count as word starts.
	a, _, _ := fuzzy.Match("fb", "FooBar")
	b, _, _ := fuzzy.Match("fb", "foobar")
	if a <= b {
		t.Fatalf("FooBar (%d) should beat foobar (%d)", a, b)
	}
}
