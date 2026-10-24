package core

import (
	"testing"

	"github.com/admirable-oss/hive/internal/branch"
	"github.com/admirable-oss/hive/pkg/hiveapi"
)

func TestBranchesAndTheirOverlapsNotifyOnce(t *testing.T) {
	list := []hiveapi.Branch{{
		ID: "a", Name: "agent/a", Base: "main", State: branch.StateWorking, Files: 2, Dirty: true,
		Owner:    &branch.Owner{Agent: "p1", Kind: "claude", Plan: "3f2a", Task: "api"},
		Overlaps: []branch.Overlap{{With: "b", Branch: "agent/b", Files: []string{"x.go"}, Conflicts: []string{"x.go"}}},
		Comments: []branch.Comment{{ID: 1, Body: "hm"}},
		Gates:    []branch.GateResult{{Name: "test", Passed: true}},
	}}
	got := branchesOf(list)
	if len(got) != 1 || got[0].Task != "3f2a/api" || got[0].Kind != "claude" || got[0].Pending != 1 ||
		len(got[0].Overlaps) != 1 || got[0].Overlaps[0].Conflicts[0] != "x.go" || !got[0].Gates[0].Passed {
		t.Fatalf("view model: %+v", got)
	}

	h := &Hub{settings: DefaultSettings()}
	ws := Workspace{Branches: got}
	if n := h.overlapsLocked(ws); len(n) != 0 {
		t.Fatalf("the first snapshot announces nothing: %+v", n)
	}
	if n := h.overlapsLocked(ws); len(n) != 0 {
		t.Fatalf("known overlaps are not announced again: %+v", n)
	}
	ws.Branches = append(ws.Branches, Branch{ID: "c", Name: "agent/c", Overlaps: []BranchOverlap{{Branch: "agent/d", Files: []string{"y.go"}, Conflicts: []string{}}}})
	n := h.overlapsLocked(ws)
	if len(n) != 1 || n[0].Title != "agent/c · agent/d" || n[0].Body != "both change y.go" {
		t.Fatalf("a new overlap: %+v", n)
	}
}
