// Package distribution holds data files shipped inside the hive binary.
package distribution

import "embed"

// AgentDetection holds the built-in agent manifests
// (agent-detection/*.toml); see agent-detection/README.md.
//
//go:embed agent-detection/*.toml
var AgentDetection embed.FS
