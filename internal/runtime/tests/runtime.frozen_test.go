package runtime_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/admirable-oss/hive/internal/apisurface"
	"github.com/admirable-oss/hive/internal/client"
)

// TestAPIFrozen holds the wire API to api/v1 (docs/compatibility.md):
// methods, their params and results, and the types they use.
func TestAPIFrozen(t *testing.T) {
	path := shortSock(t)
	start(t, path)
	c := client.NewService(client.Config{SocketPath: path})
	defer c.Close()
	var raw json.RawMessage
	if err := c.Call(context.Background(), "api.schema", struct{}{}, &raw); err != nil {
		t.Fatal(err)
	}
	if os.Getenv(apisurface.UpdateEnv) == "1" {
		var pretty any
		_ = json.Unmarshal(raw, &pretty)
		out, _ := json.MarshalIndent(pretty, "", "  ")
		if err := os.WriteFile("../../../api/v1/schema.json", append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lines, err := apisurface.SchemaLines(raw)
	if err != nil {
		t.Fatal(err)
	}
	apisurface.Check(t, "../../../api/v1/api.txt", lines)

	doc, err := apisurface.Markdown(raw)
	if err != nil {
		t.Fatal(err)
	}
	const docPath = "../../../docs/api.md"
	if os.Getenv(apisurface.UpdateEnv) == "1" {
		if err := os.WriteFile(docPath, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if got, err := os.ReadFile(docPath); err != nil || string(got) != doc {
		t.Errorf("docs/api.md is out of date: HIVE_UPDATE_SURFACE=1 go test ./internal/runtime/tests -run Frozen (%v)", err)
	}
}
