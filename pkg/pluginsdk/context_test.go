package pluginsdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestAPIv1Fixtures reads the shared plugin API 1 fixtures, as every SDK
// does.
func TestAPIv1Fixtures(t *testing.T) {
	for _, name := range []string{"context-action.json", "context-event.json"} {
		f, err := os.Open(filepath.Join("..", "..", "sdk", "protocol", "fixtures", "v1", name))
		if err != nil {
			t.Fatal(err)
		}
		c, err := ReadContext(f)
		_ = f.Close()
		if err != nil || c.API != 1 || !c.Has(CapHiveWrite) || c.Has(CapNetwork) {
			t.Fatalf("%s: %+v, %v", name, c, err)
		}
		if c.Trigger == "event" && (c.Event.Seq != 42 || c.Event.Type != "worktree.created") {
			t.Fatalf("event = %+v", c.Event)
		}
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "sdk", "protocol", "fixtures", "v1", "request-plugin-card.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want struct {
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(raw, &want)
	got, _ := json.Marshal(Card{
		Card: "build", Environment: "api", Rows: []CardRow{{Label: "main", Text: "passing", Tone: "success"}},
		Links: []Link{{Title: "Run", URL: "https://ci.example.com/run/1"}}, TTL: 10 * time.Minute,
	})
	if string(got) != string(want.Params) {
		t.Fatalf("card wire shape:\n got %s\nwant %s", got, want.Params)
	}
}
