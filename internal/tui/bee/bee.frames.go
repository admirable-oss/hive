package bee

const TotalFrames = 6

type WingRow struct {
	Left  string
	Right string
}

type Frame struct {
	Row0 WingRow
	Row1 WingRow
	Row2 WingRow
	Row3 WingRow
}

// FlapFrames contains the 6 frames of the wing flapping cycle.
// The bee body remains stationary while the wings flap smoothly.
var FlapFrames = [TotalFrames]Frame{
	// Frame 0: Wings high / lifted
	{
		Row0: WingRow{Left: " ▄▀", Right: "▀▄ "},
		Row1: WingRow{Left: "▀▀▄", Right: "▄▀▀"},
		Row2: WingRow{Left: "▀▀▀", Right: "▀▀▀"},
		Row3: WingRow{Left: "   ", Right: "   "},
	},
	// Frame 1: Wings resting / horizontal (baseline art)
	{
		Row0: WingRow{Left: "   ", Right: "   "},
		Row1: WingRow{Left: "▀▀▄", Right: "▄▀▀"},
		Row2: WingRow{Left: "▀▀▀", Right: "▀▀▀"},
		Row3: WingRow{Left: "   ", Right: "   "},
	},
	// Frame 2: Wings angled downwards
	{
		Row0: WingRow{Left: "   ", Right: "   "},
		Row1: WingRow{Left: " ▀▄", Right: "▄▀ "},
		Row2: WingRow{Left: "▀▀▄", Right: "▄▀▀"},
		Row3: WingRow{Left: "   ", Right: "   "},
	},
	// Frame 3: Wings low flap
	{
		Row0: WingRow{Left: "   ", Right: "   "},
		Row1: WingRow{Left: "   ", Right: "   "},
		Row2: WingRow{Left: " ▄▀", Right: "▀▄ "},
		Row3: WingRow{Left: "▀▀ ", Right: " ▀▀"},
	},
	// Frame 4: Returning upwards (similar to Frame 2)
	{
		Row0: WingRow{Left: "   ", Right: "   "},
		Row1: WingRow{Left: " ▀▄", Right: "▄▀ "},
		Row2: WingRow{Left: "▀▀▄", Right: "▄▀▀"},
		Row3: WingRow{Left: "   ", Right: "   "},
	},
	// Frame 5: Returning to baseline (similar to Frame 1)
	{
		Row0: WingRow{Left: "   ", Right: "   "},
		Row1: WingRow{Left: "▀▀▄", Right: "▄▀▀"},
		Row2: WingRow{Left: "▀▀▀", Right: "▀▀▀"},
		Row3: WingRow{Left: "   ", Right: "   "},
	},
}

// FoldedFrame is displayed when disconnected: wings tucked, inactive.
var FoldedFrame = Frame{
	Row0: WingRow{Left: "   ", Right: "   "},
	Row1: WingRow{Left: "   ", Right: "   "},
	Row2: WingRow{Left: " ▄ ", Right: " ▄ "},
	Row3: WingRow{Left: "   ", Right: "   "},
}
