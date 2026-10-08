package jsonfile_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/goleak"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

type doc struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc.json")
	want := doc{Name: "hive", Count: 3}
	if err := jsonfile.Write(path, want); err != nil {
		t.Fatal(err)
	}
	var got doc
	if err := jsonfile.Read(path, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestWriteReplacesAndLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.json")
	for i := range 3 {
		if err := jsonfile.Write(path, doc{Count: i}); err != nil {
			t.Fatal(err)
		}
	}
	var got doc
	if err := jsonfile.Read(path, &got); err != nil || got.Count != 2 {
		t.Fatalf("got %+v, %v; want the last write", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected only doc.json, found %d entries", len(entries))
	}
}

func TestWriteErrors(t *testing.T) {
	tests := []struct {
		name string
		path string
		v    any
	}{
		{"missing directory", filepath.Join(t.TempDir(), "nope", "doc.json"), doc{}},
		{"unencodable value", filepath.Join(t.TempDir(), "doc.json"), make(chan int)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := jsonfile.Write(tt.path, tt.v); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestReadErrors(t *testing.T) {
	dir := t.TempDir()
	var v doc
	if err := jsonfile.Read(filepath.Join(dir, "missing.json"), &v); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: got %v, want fs.ErrNotExist", err)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := jsonfile.Read(bad, &v); err == nil {
		t.Fatal("corrupt file: expected a decode error")
	}
}
