package bee

import "time"

type State string

const (
	StateIdle         State = "idle"
	StateActive       State = "active"
	StateDisconnected State = "disconnected"
)

type Model struct {
	State      State
	FrameIndex int
	PauseCount int
}

func New() Model {
	return Model{
		State: StateIdle,
	}
}

func (m *Model) SetState(s State) {
	if m.State != s {
		m.State = s
		m.FrameIndex = 0
		m.PauseCount = 0
	}
}

// Tick advances the animation frame based on the current state.
func (m *Model) Tick() {
	if m.State == StateDisconnected {
		m.FrameIndex = 0
		return
	}

	// Active state: continuous flap
	if m.State == StateActive {
		m.FrameIndex = (m.FrameIndex + 1) % TotalFrames
		return
	}

	// Idle state: flap flap -> pause -> flap flap -> pause
	// Cycle: 0, 1, 2, 3, 4, 5 (flap 1), 0, 1, 2, 3, 4, 5 (flap 2), then pause for 6 ticks
	if m.PauseCount > 0 {
		m.PauseCount--
		m.FrameIndex = 1 // resting frame
		return
	}

	m.FrameIndex++
	if m.FrameIndex >= TotalFrames*2 {
		m.FrameIndex = 1
		m.PauseCount = 8 // pause duration
	}
}

// FrameDelay returns the tick duration for the current state.
func (m *Model) FrameDelay() time.Duration {
	switch m.State {
	case StateActive:
		return 120 * time.Millisecond
	case StateDisconnected:
		return 500 * time.Millisecond
	default: // Idle
		return 150 * time.Millisecond
	}
}
