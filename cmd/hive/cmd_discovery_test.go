package main

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/discovery"
	"github.com/admirable-oss/hive/internal/remote"
)

func TestLocalName(t *testing.T) {
	for in, want := range map[string]string{
		"studio":                    "studio",
		"laptop (2)":                "laptop-2",
		"GPU box":                   "GPU-box",
		"me@192.168.1.7":            "192.168.1.7",
		"ssh://me@[2001:db8::7]:22": "2001-db8-7",
		"ssh://box.lan:2222":        "box.lan",
		"ünï":                       "n",
	} {
		got := localName(in)
		if got != want {
			t.Errorf("localName(%q) = %q, want %q", in, got, want)
		}
		if got != "" && remote.ValidName(got) != nil {
			t.Errorf("localName(%q) = %q is not a valid machine name", in, got)
		}
	}
	if got := sshUser("ssh://me@[::1]:2222"); got != "me" {
		t.Fatal(got)
	}
	if got := sshUser("box"); got != "" {
		t.Fatal(got)
	}
}

func TestPickRuntime(t *testing.T) {
	a := discovery.Record{Name: "studio", ID: "aaaabbbbccccddddeeeeffffgg"}
	b := discovery.Record{Name: "Studio", ID: "zzzzbbbbccccddddeeeeffffgg"}
	c := discovery.Record{Name: "laptop", ID: "qqqqbbbbccccddddeeeeffffgg", Addrs: []netip.Addr{netip.MustParseAddr("10.0.0.2")}}
	found := []discovery.Record{a, b, c}
	if r, err := pickRuntime(found, "LAPTOP"); err != nil || r.ID != c.ID {
		t.Fatalf("by name: %v %v", r, err)
	}
	if r, err := pickRuntime(found, "zzzz"); err != nil || r.ID != b.ID {
		t.Fatalf("by ID: %v %v", r, err)
	}
	if _, err := pickRuntime(found, "studio"); err == nil || !strings.Contains(err.Error(), "by its ID") {
		t.Fatalf("an ambiguous name: %v", err)
	}
	if _, err := pickRuntime(found, "zz"); err == nil {
		t.Fatal("a too-short ID prefix matched")
	}
	if _, err := pickRuntime(found, "nowhere"); err == nil || !strings.Contains(err.Error(), "machine discover") {
		t.Fatalf("not found: %v", err)
	}
	if got := pairArg(discovery.Record{Name: "laptop (2)", ID: c.ID}); got != c.ID {
		t.Fatalf("pairArg for a name that needs quoting: %q", got)
	}
}

func TestMachineEntries(t *testing.T) {
	saved := []remote.Machine{{Name: "studio", ID: "aaaabbbbccccddddeeeeffffgg"}, {Name: "ci", Host: "ci"}}
	near := []nearbyRuntime{
		{Record: discovery.Record{Name: "studio", ID: "aaaabbbbccccddddeeeeffffgg"}, Machine: "studio"},
		{Record: discovery.Record{Name: "new", ID: "qqqqbbbbccccddddeeeeffffgg", Port: 22, Addrs: []netip.Addr{netip.MustParseAddr("10.0.0.3")}}},
	}
	es := machineEntries(saved, near)
	if len(es) != 3 || !es[0].Nearby || es[0].State != remote.StateTrusted || es[1].Nearby || es[1].State != remote.StateConfigured {
		t.Fatalf("entries %+v", es)
	}
	if e := es[2]; e.State != remote.StateDiscovered || e.Host != "10.0.0.3" || e.Announcement == nil || e.ID != "" {
		t.Fatalf("a discovered entry %+v", e)
	}
}
