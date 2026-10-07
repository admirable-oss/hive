package bee_test

import (
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/tui/bee"
)

func TestBee_Animation(t *testing.T) {
	m := bee.New()
	if m.State != bee.StateIdle {
		t.Fatalf("expected StateIdle, got %s", m.State)
	}

	// Active state advances frames
	m.SetState(bee.StateActive)
	for i := range bee.TotalFrames {
		if m.FrameIndex != i {
			t.Errorf("expected frame %d, got %d", i, m.FrameIndex)
		}
		m.Tick()
	}
	if m.FrameIndex != 0 {
		t.Errorf("expected wrap to 0, got %d", m.FrameIndex)
	}

	// Disconnected state holds frame 0
	m.SetState(bee.StateDisconnected)
	m.Tick()
	if m.FrameIndex != 0 {
		t.Errorf("expected frame 0 in disconnected, got %d", m.FrameIndex)
	}

	// Idle state advances with pauses
	m.SetState(bee.StateIdle)
	for range 30 {
		m.Tick()
	}
}

func TestBee_View(t *testing.T) {
	m := bee.New()
	for _, state := range []bee.State{bee.StateIdle, bee.StateActive, bee.StateDisconnected} {
		m.SetState(state)
		for f := range bee.TotalFrames {
			m.FrameIndex = f
			out := m.View()
			if out == "" {
				t.Fatalf("expected non-empty view for state %s, frame %d", state, f)
			}
			lines := strings.Split(out, "\n")
			if len(lines) != 7 {
				t.Errorf("expected 7 lines of art, got %d", len(lines))
			}
		}
	}
}
