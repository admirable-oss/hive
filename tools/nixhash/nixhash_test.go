package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestFlakeVendorHash fails when go.mod changed and flake.nix still names
// the old vendor hash: `go run ./tools/nixhash` prints the new one.
func TestFlakeVendorHash(t *testing.T) {
	if testing.Short() {
		t.Skip("vendors every module")
	}
	flake, err := os.ReadFile(filepath.Join("..", "..", "flake.nix"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`vendorHash = "([^"]+)"`).FindSubmatch(flake)
	if m == nil {
		t.Fatal("flake.nix has no vendorHash")
	}
	got, err := VendorHash(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if string(m[1]) != got {
		t.Fatalf("flake.nix vendorHash is %s; the modules hash to %s (go run ./tools/nixhash)", m[1], got)
	}
}

// TestNARHash pins the serialization on a small tree.
func TestNARHash(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	a, err := NARHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "run.sh"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := NARHash(dir)
	if a == b {
		t.Fatal("the executable bit must change the hash")
	}
}
