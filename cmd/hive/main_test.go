package main

import (
	"io"
	"strings"
	"testing"
)

func TestDetachReaderStopsAtCtrlBracket(t *testing.T) {
	r := detachReader{strings.NewReader("ls -la\r\x1dignored")}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ls -la\r" {
		t.Fatalf("got %q, want input up to the detach key", got)
	}
}

func TestLookupResolvesAliases(t *testing.T) {
	for alias, name := range map[string]string{"tui": "ui", "env": "environment", "ps": "process", "daemon": "daemon"} {
		if c, ok := lookup(alias); !ok || c.name != name {
			t.Errorf("lookup(%q) = %q, %v; want %q", alias, c.name, ok, name)
		}
	}
	if _, ok := lookup("nope"); ok {
		t.Error("unknown commands must not resolve")
	}
}
