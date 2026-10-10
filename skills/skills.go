// Package skills holds the agent skills shipped inside the hive binary.
package skills

import _ "embed"

// Hive is skills/hive/SKILL.md: how an agent drives other agents through
// Hive. `hive --skill` prints it.
//
//go:embed hive/SKILL.md
var Hive string
