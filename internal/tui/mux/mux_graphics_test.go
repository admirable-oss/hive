package mux

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/vt"
)

func testImagePNG(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0xff // opaque white
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// kittyUpload is a program printing a PNG with a=T (transmit and display
// cols×rows cells at the cursor), chunked as Kitty requires.
func kittyUpload(data []byte, id, cols, rows int, cursor vt.Cursor) []vt.GraphicsEvent {
	encoded := base64.StdEncoding.EncodeToString(data)
	var out []vt.GraphicsEvent
	for off := 0; off < len(encoded); off += 4096 {
		end := min(off+4096, len(encoded))
		more := 0
		if end < len(encoded) {
			more = 1
		}
		head := fmt.Sprintf("m=%d", more)
		if off == 0 {
			head = fmt.Sprintf("a=T,f=100,i=%d,c=%d,r=%d,q=2,m=%d", id, cols, rows, more)
		}
		out = append(out, vt.GraphicsEvent{Command: []byte("\x1b_G" + head + ";" + encoded[off:end] + "\x1b\\"), Cursor: cursor})
	}
	return out
}

func graphicsView(events ...vt.GraphicsEvent) *view {
	v := &view{procID: "p"}
	v.graphicsState.mem = newGraphicsMemory()
	for _, e := range events {
		v.graphics = append(v.graphics, e)
		v.graphicsBytes += len(e.Command)
		v.graphicsState.mem.queued.take(len(e.Command))
	}
	return v
}

var paneRect = layout.Rect{W: 40, H: 12}

func TestGraphicsPlan_KittyRemapsAndTranslates(t *testing.T) {
	data := testImagePNG(t, 64, 64) // > one chunk once padded below
	data = append(data, make([]byte, 6000)...)
	v := graphicsView(kittyUpload(data, 5, 4, 2, vt.Cursor{X: 3, Y: 2})...)
	plan, replies := v.graphicsPlan(paneRect, 10, 1, graphicsKitty, 8, 16, true)
	if len(replies) != 0 || len(plan.uploads) < 2 {
		t.Fatalf("uploads %d, replies %d", len(plan.uploads), len(replies))
	}
	if strings.Contains(plan.uploads[0], "i=5,") || !strings.Contains(plan.uploads[0], "a=t") {
		t.Fatalf("first upload %.60q keeps the program's id or still displays", plan.uploads[0])
	}
	if len(plan.placements) != 1 {
		t.Fatalf("placements = %v", plan.placements)
	}
	for _, p := range plan.placements {
		if p.col != 13 || p.row != 3 || p.cols != 4 || p.rows != 2 {
			t.Fatalf("placement %+v, want pane (3,2) moved to the screen's (13,3)", p)
		}
		if seq := placementSequence(p); !strings.HasPrefix(seq, "\x1b[4;14H\x1b_G") {
			t.Fatalf("placement sequence %q", seq)
		}
	}
	if v.graphicsBytes != 0 || v.graphicsState.mem.queued.used.Load() != 0 {
		t.Fatalf("drawn events still held: %d bytes, budget %d", v.graphicsBytes, v.graphicsState.mem.queued.used.Load())
	}
}

func TestGraphicsPlan_PartialPlacementIsSuppressed(t *testing.T) {
	v := graphicsView(kittyUpload(testImagePNG(t, 8, 8), 1, 10, 2, vt.Cursor{X: 35, Y: 0})...)
	plan, _ := v.graphicsPlan(paneRect, 0, 0, graphicsKitty, 8, 16, true)
	if len(plan.placements) != 0 {
		t.Fatalf("a placement crossing the pane's edge was drawn: %v", plan.placements)
	}
}

func TestGraphicsPlan_CellsDrawHalfBlocks(t *testing.T) {
	v := graphicsView(kittyUpload(testImagePNG(t, 8, 8), 1, 4, 2, vt.Cursor{X: 1, Y: 1})...)
	plan, _ := v.graphicsPlan(paneRect, 0, 0, graphicsCells, 0, 0, true)
	if len(plan.cells) != 1 || plan.cells[0].image == nil {
		t.Fatalf("cells = %+v", plan.cells)
	}
	scr := uv.NewScreenBuffer(40, 12)
	applyCellGraphics(scr, plan.cells)
	cell := scr.CellAt(1, 1)
	if cell == nil || cell.Content != "▀" || cell.Style.Fg != (color.RGBA{0xff, 0xff, 0xff, 0xff}) {
		t.Fatalf("cell = %+v", cell)
	}
	if c := scr.CellAt(5, 1); c != nil && c.Content == "▀" {
		t.Fatal("drew outside the placement")
	}
}

func TestGraphicsPlan_TextNamesImagesWithoutDecoding(t *testing.T) {
	v := graphicsView(kittyUpload(testImagePNG(t, 8, 8), 1, 12, 2, vt.Cursor{X: 2, Y: 3})...)
	plan, _ := v.graphicsPlan(paneRect, 0, 0, graphicsText, 0, 0, true)
	if len(plan.cells) != 1 || plan.cells[0].image != nil {
		t.Fatalf("cells = %+v", plan.cells)
	}
	if v.graphicsState.imageTotal != 0 {
		t.Fatal("text mode decoded the image")
	}
	scr := uv.NewScreenBuffer(40, 12)
	applyCellGraphics(scr, plan.cells)
	var got strings.Builder
	for x := 2; x < 2+len(imagePlaceholder); x++ {
		got.WriteString(scr.CellAt(x, 3).Content)
	}
	if got.String() != imagePlaceholder {
		t.Fatalf("row reads %q", got.String())
	}
}

func TestGraphicsPlan_TextClipsToNarrowPlacement(t *testing.T) {
	v := graphicsView(kittyUpload(testImagePNG(t, 4, 4), 1, 3, 1, vt.Cursor{})...)
	plan, _ := v.graphicsPlan(paneRect, 0, 0, graphicsText, 0, 0, true)
	scr := uv.NewScreenBuffer(40, 12)
	applyCellGraphics(scr, plan.cells)
	if c := scr.CellAt(3, 0); c != nil && c.Content == "e" {
		t.Fatal("the placeholder ran past its three columns")
	}
}

func TestGraphicsPlan_InvalidImageFallsBackToText(t *testing.T) {
	bad := []byte("\x89PNG\r\n\x1a\n not really")
	v := graphicsView(kittyUpload(bad, 1, 8, 1, vt.Cursor{})...)
	plan, _ := v.graphicsPlan(paneRect, 0, 0, graphicsCells, 0, 0, true)
	// The relay accepts it (its base64 is valid) but it does not decode:
	// the placement is named as text rather than drawn or dropped.
	if len(plan.cells) != 1 || plan.cells[0].image != nil {
		t.Fatalf("cells = %+v", plan.cells)
	}
	scr := uv.NewScreenBuffer(40, 12)
	applyCellGraphics(scr, plan.cells)
	if scr.CellAt(0, 0).Content != "[" {
		t.Fatal("no placeholder drawn")
	}
}

func TestGraphicsPlan_SixelAndItermEncodePlacements(t *testing.T) {
	for _, tc := range []struct {
		protocol graphicsProtocol
		want     string
	}{
		{graphicsSixel, "\x1bP"},
		{graphicsIterm, "\x1b]1337;File=inline=1"},
	} {
		v := graphicsView(kittyUpload(testImagePNG(t, 16, 16), 1, 2, 1, vt.Cursor{X: 1})...)
		plan, _ := v.graphicsPlan(paneRect, 5, 0, tc.protocol, 8, 16, true)
		seqs := plan.sixels
		if tc.protocol == graphicsIterm {
			seqs = plan.iterm
		}
		if len(seqs) != 1 || !strings.HasPrefix(seqs[0], "\x1b[1;7H"+tc.want) {
			t.Fatalf("protocol %d: sequences %.40q", tc.protocol, seqs)
		}
	}
}

func TestGraphicsPlan_OffDrawsNothing(t *testing.T) {
	v := graphicsView(kittyUpload(testImagePNG(t, 8, 8), 1, 4, 2, vt.Cursor{})...)
	plan, _ := v.graphicsPlan(paneRect, 0, 0, renderProtocol(ImageRenderOff, graphicsKitty), 8, 16, true)
	if len(plan.uploads)+len(plan.cells)+len(plan.sixels)+len(plan.iterm)+len(plan.placements) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestGraphicsPlan_UnsafeAndMalformedCommandsAreIgnored(t *testing.T) {
	events := []vt.GraphicsEvent{
		{Command: []byte("\x1b_Ga=T,t=f,f=100,c=2,r=1;L2V0Yy9wYXNzd2Q=\x1b\\")}, // a file on the agent's machine
		{Command: []byte("\x1b_Ga=T,t=s,f=100,c=2,r=1;c2htLW5hbWU=\x1b\\")},     // shared memory
		{Command: []byte("\x1b_Ga=T,f=100,c=2,r=1;!!!not base64\x1b\\")},
		{Command: []byte("\x1b_Ga=T,a=t;AAAA\x1b\\")}, // duplicate key
		{Command: []byte("not an apc at all")},
		{Command: []byte("\x1b_Ga=d,d=A;\x1b\\")}, // delete everything in the outer terminal
	}
	v := graphicsView(events...)
	for _, protocol := range []graphicsProtocol{graphicsKitty, graphicsCells} {
		plan, replies := v.graphicsPlan(paneRect, 0, 0, protocol, 8, 16, true)
		if len(plan.uploads)+len(plan.placements)+len(plan.cells)+len(replies) != 0 {
			t.Fatalf("protocol %d: plan %+v replies %q", protocol, plan, replies)
		}
	}
}

func TestGraphicsPlan_QueryWithoutKittyIsRefused(t *testing.T) {
	v := graphicsView(vt.GraphicsEvent{Command: []byte("\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\")})
	_, replies := v.graphicsPlan(paneRect, 0, 0, graphicsCells, 0, 0, true)
	if len(replies) != 1 || !strings.Contains(string(replies[0]), "i=31;ENOTSUP") {
		t.Fatalf("replies = %q", replies)
	}
}

func TestGraphicsPlan_ScrollClearsPlacements(t *testing.T) {
	v := graphicsView(kittyUpload(testImagePNG(t, 8, 8), 1, 4, 2, vt.Cursor{})...)
	if plan, _ := v.graphicsPlan(paneRect, 0, 0, graphicsKitty, 8, 16, true); len(plan.placements) != 1 {
		t.Fatal("no placement before the scroll")
	}
	v.graphicsScrolled = true
	if plan, _ := v.graphicsPlan(paneRect, 0, 0, graphicsKitty, 8, 16, true); len(plan.placements) != 0 {
		t.Fatalf("placements after a scroll: %v", plan.placements)
	}
}

func TestGraphicsMemory_SharedBudgetBoundsAllViews(t *testing.T) {
	mem := graphicsMemory{queued: &graphicsBudget{limit: 100}, images: &graphicsBudget{limit: 64 * 64 * 4}}
	if !mem.queued.take(60) || mem.queued.take(60) || !mem.queued.take(40) {
		t.Fatal("the queued budget does not hold its limit")
	}
	mem.queued.give(100)
	if mem.queued.used.Load() != 0 {
		t.Fatalf("used = %d after giving everything back", mem.queued.used.Load())
	}
	// Two views decoding images share one budget: the second view's image
	// does not fit, so it is named as text instead of drawn.
	a, b := graphicsView(kittyUpload(testImagePNG(t, 64, 64), 1, 4, 2, vt.Cursor{})...), graphicsView(kittyUpload(testImagePNG(t, 64, 64), 1, 4, 2, vt.Cursor{})...)
	a.graphicsState.mem, b.graphicsState.mem = mem, mem
	pa, _ := a.graphicsPlan(paneRect, 0, 0, graphicsCells, 0, 0, true)
	pb, _ := b.graphicsPlan(paneRect, 0, 0, graphicsCells, 0, 0, true)
	if len(pa.cells) != 1 || pa.cells[0].image == nil || len(pb.cells) != 1 || pb.cells[0].image != nil {
		t.Fatalf("first %+v second %+v", pa.cells, pb.cells)
	}
	a.releaseGraphics()
	if mem.images.used.Load() != 0 {
		t.Fatalf("released view still holds %d image bytes", mem.images.used.Load())
	}
	var nilBudget *graphicsBudget
	if !nilBudget.take(1 << 40) {
		t.Fatal("a nil budget is unlimited")
	}
}

// BenchmarkGraphicsFrame is one frame's graphics work for a pane showing a
// 512×512 image in 40×20 cells (decoded once, as on redraws): the plan, and
// for cells drawing them.
func BenchmarkGraphicsFrame(b *testing.B) {
	data := testImagePNG(b, 512, 512)
	rect := layout.Rect{W: 80, H: 24}
	for _, tc := range []struct {
		name     string
		protocol graphicsProtocol
	}{{"kitty", graphicsKitty}, {"sixel", graphicsSixel}, {"iterm", graphicsIterm}, {"cells", graphicsCells}, {"text", graphicsText}} {
		b.Run(tc.name, func(b *testing.B) {
			v := graphicsView(kittyUpload(data, 1, 40, 20, vt.Cursor{})...)
			v.graphicsPlan(rect, 0, 0, tc.protocol, 8, 16, true)
			scr := uv.NewScreenBuffer(80, 24)
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				x := i % 2 // a move each frame, so every protocol redoes its placement
				plan, _ := v.graphicsPlan(rect, x, 0, tc.protocol, 8, 16, true)
				applyCellGraphics(scr, plan.cells)
			}
		})
	}
}

// BenchmarkGraphicsUpload is the cost of a new 512×512 PNG reaching the UI:
// parsing its chunks, and decoding it where the protocol needs pixels.
func BenchmarkGraphicsUpload(b *testing.B) {
	data := testImagePNG(b, 512, 512)
	events := kittyUpload(data, 1, 40, 20, vt.Cursor{})
	for _, tc := range []struct {
		name     string
		protocol graphicsProtocol
	}{{"kitty", graphicsKitty}, {"cells", graphicsCells}} {
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				v := graphicsView(events...)
				v.graphicsPlan(paneRect, 0, 0, tc.protocol, 8, 16, true)
			}
		})
	}
}
