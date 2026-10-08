package logging_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.uber.org/goleak"

	"github.com/admirable-oss/hive/internal/logging"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestRotatingFileRollsOverAndKeepsBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "daemon.log")
	r, err := logging.OpenRotating(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 39) + "\n" // 40 bytes: two fit, the third rotates
	for i := range 10 {
		if _, err := fmt.Fprintf(r, "%d%s", i, line[1:]); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	read := func(p string) string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := read(path); !strings.HasPrefix(got, "8") || strings.Count(got, "\n") != 2 {
		t.Fatalf("current file = %q, want lines 8 and 9", got)
	}
	if got := read(path + ".1"); !strings.HasPrefix(got, "6") {
		t.Fatalf("backup 1 = %q, want lines 6 and 7", got)
	}
	if got := read(path + ".2"); !strings.HasPrefix(got, "4") {
		t.Fatalf("backup 2 = %q, want lines 4 and 5", got)
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("only two backups may be kept, stat .3: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("log file mode = %v, want 0600", perm)
	}
}

func TestRotatingFileWithoutBackupsTruncates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	r, err := logging.OpenRotating(path, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, s := range []string{"12345678\n", "abcdefgh\n"} {
		if _, err := r.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(path); string(b) != "abcdefgh\n" {
		t.Fatalf("got %q", b)
	}
}

func TestRotatingFileAppendsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	for _, s := range []string{"one\n", "two\n"} {
		r, err := logging.OpenRotating(path, 1<<20, 1)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = r.Write([]byte(s))
		_ = r.Close()
	}
	if b, _ := os.ReadFile(path); string(b) != "one\ntwo\n" {
		t.Fatalf("got %q", b)
	}
}

func TestRotatingFileConcurrentWritesStayWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	r, err := logging.OpenRotating(path, 4096, 50)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 200 {
				fmt.Fprintf(r, "g%d-%04d %s\n", g, i, strings.Repeat("=", 20))
			}
		})
	}
	wg.Wait()
	_ = r.Close()

	files, _ := filepath.Glob(path + "*")
	lines := 0
	for _, f := range files {
		b, _ := os.ReadFile(f)
		for l := range strings.SplitSeq(strings.TrimSuffix(string(b), "\n"), "\n") {
			if !strings.HasSuffix(l, strings.Repeat("=", 20)) {
				t.Fatalf("torn line %q in %s", l, f)
			}
			lines++
		}
	}
	if lines != 1600 {
		t.Fatalf("got %d lines, want 1600", lines)
	}
}

func TestWriteAfterCloseFails(t *testing.T) {
	r, err := logging.OpenRotating(filepath.Join(t.TempDir(), "a.log"), 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if _, err := r.Write([]byte("x")); err == nil {
		t.Fatal("expected an error writing to a closed file")
	}
	if err := r.Close(); err != nil {
		t.Fatalf("double close should be a no-op, got %v", err)
	}
}

func TestNewWritesJSONToFileAtLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.log")
	log, closeFn, err := logging.New(logging.Config{Level: slog.LevelWarn, Format: logging.FormatJSON, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	log.Info("dropped")
	log.Warn("kept", "env", "dev")
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("want exactly the warn record, got %q", data)
	}
	var rec map[string]any
	if err := json.Unmarshal(lines[0], &rec); err != nil {
		t.Fatal(err)
	}
	if rec["msg"] != "kept" || rec["env"] != "dev" {
		t.Fatalf("unexpected record %v", rec)
	}
}

func TestNewWithoutOutputsDiscards(t *testing.T) {
	log, closeFn, err := logging.New(logging.Config{})
	if err != nil {
		t.Fatal(err)
	}
	log.Error("nowhere")
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}
	if logging.OrDiscard(nil) == nil {
		t.Fatal("OrDiscard(nil) must return a usable logger")
	}
}

func TestParseLevelAndFormat(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warning", slog.LevelWarn},
		{" error ", slog.LevelError},
	} {
		if got, err := logging.ParseLevel(tt.in); err != nil || got != tt.want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
		}
	}
	if _, err := logging.ParseLevel("loud"); err == nil {
		t.Error("ParseLevel should reject unknown levels")
	}
	if f, err := logging.ParseFormat("JSON"); err != nil || f != logging.FormatJSON {
		t.Errorf("ParseFormat(JSON) = %v, %v", f, err)
	}
	if _, err := logging.ParseFormat("xml"); err == nil {
		t.Error("ParseFormat should reject unknown formats")
	}
}
