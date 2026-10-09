package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// keepDevBinaries is how many copies of `go run` builds stay in
// <HIVE_HOME>/bin (a daemon or shim may still run an older one).
const keepDevBinaries = 3

// stableExecutable returns a path to exe that outlives this process. A
// `go run` binary lives in a temporary go-build directory that the go
// command deletes when the run ends, while the daemon it starts keeps
// starting shims, and telling agents where hive is (HIVE_BIN), from its
// own path. Such a binary is copied to <HIVE_HOME>/bin, named by its
// content, and the daemon is started from the copy.
func (a *app) stableExecutable(exe string) (string, error) {
	if !isGoRunBinary(exe) {
		return exe, nil
	}
	src, err := os.Open(exe)
	if err != nil {
		return "", fmt.Errorf("read the hive binary: %w", err)
	}
	defer src.Close()
	h := sha256.New()
	if _, err := io.Copy(h, src); err != nil {
		return "", fmt.Errorf("read the hive binary: %w", err)
	}
	dir := filepath.Join(a.base, "bin")
	dst := filepath.Join(dir, "hive-dev-"+hex.EncodeToString(h.Sum(nil))[:12])
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".hive-dev-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("copy the hive binary: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", err
	}
	pruneDevBinaries(dir, dst)
	return dst, nil
}

// isGoRunBinary reports whether exe was built by `go run`
// (…/go-build<n>/b001/exe/<name>).
func isGoRunBinary(exe string) bool {
	return filepath.Base(filepath.Dir(exe)) == "exe" && strings.Contains(exe, string(filepath.Separator)+"go-build")
}

// pruneDevBinaries removes all but the newest copies. Removing a binary a
// process still runs is safe: the process keeps its open file.
func pruneDevBinaries(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type bin struct {
		path string
		mod  int64
	}
	var bins []bin
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "hive-dev-") {
			continue
		}
		if info, err := e.Info(); err == nil {
			bins = append(bins, bin{filepath.Join(dir, e.Name()), info.ModTime().UnixNano()})
		}
	}
	slices.SortFunc(bins, func(a, b bin) int { return int(b.mod - a.mod) })
	for i, b := range bins {
		if i >= keepDevBinaries && b.path != keep {
			_ = os.Remove(b.path)
		}
	}
}
