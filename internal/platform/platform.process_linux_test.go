//go:build linux

package platform

import "testing"

func TestParseStatHandlesHostileCommandNames(t *testing.T) {
	// The command name contains spaces and a ')' to defeat naive splitting.
	stat := []byte("4242 (evil ) name) S 1 4242 4242 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 987654 1000 10 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 3 0 0 0 0 0")
	pgid, ticks, err := parseStat(stat)
	if err != nil {
		t.Fatal(err)
	}
	if pgid != 4242 || ticks != 987654 {
		t.Fatalf("got pgid %d ticks %d, want 4242 and 987654", pgid, ticks)
	}
	if _, _, err := parseStat([]byte("garbage")); err == nil {
		t.Fatal("expected an error for malformed input")
	}
}
