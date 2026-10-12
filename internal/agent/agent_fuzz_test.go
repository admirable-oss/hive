package agent

import (
	"testing"

	"github.com/admirable-oss/hive/distribution"
	"github.com/admirable-oss/hive/internal/vt"
)

// FuzzParseManifest: no agent manifest panics the parser, and an accepted
// one can classify a screen.
func FuzzParseManifest(f *testing.F) {
	for _, name := range []string{"agent-detection/claude.toml", "agent-detection/codex.toml", "agent-detection/aider.toml"} {
		if data, err := distribution.AgentDetection.ReadFile(name); err == nil {
			f.Add(data)
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := ParseManifest(data, "fuzz.toml")
		if err != nil {
			return
		}
		_ = m.Matches([]string{"claude", "--resume", "x"})
		_, _ = m.ResumeCommand([]string{"node", "cli.js"}, "id")
		screen := vt.NewScreen(80, 24)
		_ = m.Classify(screen)
	})
}
