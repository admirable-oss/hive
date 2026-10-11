package main

import (
	"slices"
	"testing"
)

func TestRemoteArgs(t *testing.T) {
	args := []string{"--remote", "me@box", "--session=s", "--yes", "agent", "prompt", "--json", "p1", "--", "--remote", "x"}
	r, ok := parseRemoteArgs(args)
	if !ok || r.host != "me@box" || r.session != "s" || !r.yes || r.machine != "" {
		t.Fatalf("parsed %+v, %v", r, ok)
	}
	if got := withoutRemoteArgs(args); !slices.Equal(got, []string{"agent", "prompt", "--json", "p1", "--", "--remote", "x"}) {
		t.Fatalf("stripped %q", got)
	}
	if r, ok := parseRemoteArgs([]string{"--machine=gpu", "ui"}); !ok || r.machine != "gpu" {
		t.Fatalf("--machine= %+v", r)
	}
	if _, ok := parseRemoteArgs([]string{"ps", "start", "dev", "--", "ssh", "--remote", "x"}); ok {
		t.Fatal("flags after -- belong to the command")
	}
}
