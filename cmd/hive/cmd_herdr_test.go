package main

import (
	"testing"

	"github.com/admirable-oss/hive/internal/config"
)

// TestHerdrKeysWorkInTheUI: herdr's whole default keymap, imported, is a
// keymap the multiplexer takes without a warning.
func TestHerdrKeysWorkInTheUI(t *testing.T) {
	cfg, _, err := config.ImportHerdr(config.Defaults(), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, warnings := uiOptions(cfg); len(warnings) != 0 {
		t.Fatalf("warnings: %q", warnings)
	}
}
