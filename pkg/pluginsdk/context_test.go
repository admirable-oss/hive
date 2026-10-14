package pluginsdk

import (
	"strings"
	"testing"
)

func TestReadContextMatchesPluginHost(t *testing.T) {
	got, err := ReadContext(strings.NewReader(`{"plugin":"example","trigger":"event","session":"s1","event":{"type":"process.output","data":{"id":"p1"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Plugin != "example" || got.Event == nil || got.Event.Type != "process.output" {
		t.Fatalf("unexpected context: %#v", got)
	}
	if _, err := ReadContext(strings.NewReader(`{"plugin":"example","trigger":"startup"}`)); err == nil {
		t.Fatal("expected incomplete invocation context to fail")
	}
	if _, err := ReadContext(strings.NewReader(strings.Repeat(" ", (1<<20)+1))); err == nil {
		t.Fatal("expected oversized invocation context to fail")
	}
}

func TestRunWithOptionsRejectsNilHandlerBeforeReading(t *testing.T) {
	if err := RunWithOptions(t.Context(), InitOptions{}, nil); err == nil {
		t.Fatal("expected nil handler error")
	}
}
