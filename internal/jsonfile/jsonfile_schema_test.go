package jsonfile_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

func TestCheckSchema(t *testing.T) {
	for _, schema := range []int{0, 1} {
		if err := jsonfile.CheckSchema("state.json", schema, 1); err != nil {
			t.Fatalf("schema %d: %v", schema, err)
		}
	}
	err := jsonfile.CheckSchema("state.json", 2, 1)
	if !errors.Is(err, jsonfile.ErrNewer) || !strings.Contains(err.Error(), "state.json") {
		t.Fatalf("a newer schema: %v", err)
	}
}
