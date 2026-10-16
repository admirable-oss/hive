package observer

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/admirable-oss/hive/internal/apisurface"
)

// TestObserverAPIFrozen holds the observer API to api/v1 (the
// compatibility policy): its methods, their params and results, the types
// they use, and the event types observers receive.
func TestObserverAPIFrozen(t *testing.T) {
	s := NewServer(Config{Store: NewStore(t.TempDir()), History: NewHistory(0)})
	raw, err := json.Marshal(s.Router().Schema())
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv(apisurface.UpdateEnv) == "1" {
		out, _ := json.MarshalIndent(s.Router().Schema(), "", "  ")
		if err := os.WriteFile("../../api/v1/observer-schema.json", append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lines, err := apisurface.SchemaLines(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range Types {
		lines = append(lines, "event "+typ)
	}
	apisurface.Check(t, "../../api/v1/observer.txt", lines)

	doc, err := apisurface.Markdown(raw)
	if err != nil {
		t.Fatal(err)
	}
	const docPath = "testdata/observer-reference.md"
	if os.Getenv(apisurface.UpdateEnv) == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(docPath, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if got, err := os.ReadFile(docPath); err != nil || string(got) != doc {
		t.Errorf("testdata/observer-reference.md is out of date: HIVE_UPDATE_SURFACE=1 go test ./internal/observer -run Frozen (%v)", err)
	}
}
