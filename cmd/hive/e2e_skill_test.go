package main

import (
	"testing"

	"github.com/admirable-oss/hive/skills"
)

// TestSkillFlag: `hive --skill` prints the skill as it ships, and needs
// no daemon or configuration.
func TestSkillFlag(t *testing.T) {
	c := newCLI(t)
	if out := c.must("--skill"); out != skills.Hive || out == "" {
		t.Fatalf("hive --skill printed %d bytes, not the %d of skills.Hive", len(out), len(skills.Hive))
	}
	if _, _, code := c.run("--skill", "--json"); code != 0 {
		t.Fatalf("--skill with another flag: %d", code)
	}
}
