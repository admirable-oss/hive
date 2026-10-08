package buildinfo_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/buildinfo"
)

func TestGetAlwaysHasAVersion(t *testing.T) {
	info := buildinfo.Get()
	if info.Version == "" {
		t.Fatal("version must never be empty")
	}
	if info.GoVersion != runtime.Version() || info.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Fatalf("unexpected toolchain info %+v", info)
	}
}

func TestStringTruncatesCommit(t *testing.T) {
	s := buildinfo.Info{Version: "v1.2.3", Commit: "0123456789abcdef", Date: "2026-10-08", GoVersion: "go1.26", Platform: "linux/amd64"}.String()
	want := "hive v1.2.3 (0123456789ab, 2026-10-08) go1.26 linux/amd64"
	if s != want {
		t.Fatalf("got %q, want %q", s, want)
	}
	if s := (buildinfo.Info{Version: "dev", GoVersion: "go1.26", Platform: "x/y"}).String(); strings.Contains(s, "(") {
		t.Fatalf("no commit means no parenthesis, got %q", s)
	}
}
