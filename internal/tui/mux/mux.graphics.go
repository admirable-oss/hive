package mux

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/vt"
)

var nextOuterGraphicsID atomic.Uint32

type graphicsPlacementKey struct{ image, placement uint32 }

type graphicsPlacement struct {
	image, placement uint32
	row, col         int
	rows, cols       int
	options          map[byte]string
}

type graphicsViewState struct {
	processed      int
	imageIDs       map[uint32]uint32
	images         map[uint32]image.Image
	imageBytes     map[uint32]int
	imageTotal     int
	assembler      vt.KittyGraphicsAssembler
	responseIDs    map[uint32]uint32 // outer image id -> agent image id
	placements     map[graphicsPlacementKey]graphicsPlacement
	placementReply map[graphicsPlacementKey]uint32 // outer placement -> agent placement (zero means omitted)
	nextPlacement  uint32
	uploadID       uint32
	uploadInner    uint32
	uploadPlace    *graphicsPlacement
	pendingUploads []string
	dirty          bool
	lastRect       layout.Rect
	lastX, lastY   int
	lastProtocol   graphicsProtocol
	auxSequences   []string
	auxCells       []cellGraphicsPlacement
	mem            graphicsMemory
}

// dropImages releases every decoded image.
func (s *graphicsViewState) dropImages() {
	s.mem.images.give(s.imageTotal)
	s.images, s.imageBytes, s.imageTotal = nil, nil, 0
}

// graphicsBudget bounds bytes held by all views together, on top of each
// view's own bounds, so many panes of images cannot exhaust memory. A nil
// budget is unlimited (tests that build an App by hand).
type graphicsBudget struct {
	used  atomic.Int64
	limit int64
}

func (b *graphicsBudget) take(n int) bool {
	if b == nil || n <= 0 {
		return true
	}
	if b.used.Add(int64(n)) > b.limit {
		b.used.Add(-int64(n))
		return false
	}
	return true
}

func (b *graphicsBudget) give(n int) {
	if b != nil && n > 0 {
		b.used.Add(-int64(n))
	}
}

// graphicsMemory is the UI's graphics memory: events received and not yet
// drawn, and images decoded for Sixel, iTerm2 and cell output.
type graphicsMemory struct {
	queued, images *graphicsBudget
}

const (
	maxGraphicsQueuedTotal = 64 << 20
	maxGraphicsImagesTotal = 96 << 20
)

func newGraphicsMemory() graphicsMemory {
	return graphicsMemory{
		queued: &graphicsBudget{limit: maxGraphicsQueuedTotal},
		images: &graphicsBudget{limit: maxGraphicsImagesTotal},
	}
}

const (
	maxSixelStateBytes    = 32 << 20
	maxSixelFrameBytes    = 16 << 20
	maxItermSequenceBytes = 900 << 10 // Leave room below OSC implementations' 1 MiB limit.
)

func (s *graphicsViewState) deleteSixelImage(id uint32) {
	if n := s.imageBytes[id]; n > 0 {
		s.imageTotal -= n
		s.mem.images.give(n)
		delete(s.imageBytes, id)
	}
	delete(s.images, id)
}

func (s *graphicsViewState) saveSixelImage(id uint32, img image.Image) {
	b := img.Bounds()
	n := b.Dx() * b.Dy() * 4
	s.deleteSixelImage(id)
	if n <= 0 || n > maxSixelStateBytes || n > maxSixelStateBytes-s.imageTotal || !s.mem.images.take(n) {
		return
	}
	if s.images == nil {
		s.images = make(map[uint32]image.Image)
		s.imageBytes = make(map[uint32]int)
	}
	s.images[id], s.imageBytes[id] = img, n
	s.imageTotal += n
}

type graphicsPlan struct {
	uploads    []string
	sixels     []string
	iterm      []string
	cells      []cellGraphicsPlacement
	placements map[graphicsPlacementKey]graphicsPlacement
	images     map[uint32]struct{}
}

type cellGraphicsPlacement struct {
	image                image.Image
	row, col, rows, cols int
}

type graphicsTarget struct {
	procID string
	rect   layout.Rect
	x, y   int
}

func allocOuterGraphicsID() uint32 {
	return 0x80000000 | (nextOuterGraphicsID.Add(1) & 0x7fffffff)
}

func cloneGraphicsOptions(src map[byte]string) map[byte]string {
	dst := make(map[byte]string, len(src)+2)
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func formatGraphicsCommand(options map[byte]string, payload []byte) string {
	keys := make([]int, 0, len(options))
	for k := range options {
		keys = append(keys, int(k))
	}
	sort.Ints(keys)
	var b strings.Builder
	b.Grow(16 + len(payload) + len(options)*8)
	b.WriteString("\x1b_G")
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte(byte(k))
		b.WriteByte('=')
		b.WriteString(options[byte(k)])
	}
	b.WriteByte(';')
	b.Write(payload)
	b.WriteString("\x1b\\")
	return b.String()
}

func (s *graphicsViewState) outerID(inner uint32) uint32 {
	if s.imageIDs == nil {
		s.imageIDs = make(map[uint32]uint32)
		s.responseIDs = make(map[uint32]uint32)
	}
	if id := s.imageIDs[inner]; id != 0 {
		return id
	}
	id := allocOuterGraphicsID()
	s.imageIDs[inner] = id
	s.responseIDs[id] = inner
	return id
}

func newGraphicsPlacement(c vt.KittyGraphicsCommand, imageID uint32, cursor vt.Cursor, placementID uint32) (graphicsPlacement, bool) {
	cols, errC := strconv.Atoi(c.Options['c'])
	rows, errR := strconv.Atoi(c.Options['r'])
	if errC != nil || errR != nil || cols <= 0 || rows <= 0 || cols > vt.MaxCols || rows > vt.MaxRows {
		return graphicsPlacement{}, false
	}
	return graphicsPlacement{
		image: imageID, placement: placementID,
		row: cursor.Y, col: cursor.X, rows: rows, cols: cols,
		options: cloneGraphicsOptions(c.Options),
	}, true
}

func (s *graphicsViewState) place(c vt.KittyGraphicsCommand, cursor vt.Cursor, innerID uint32) {
	imageID := s.outerID(innerID)
	key := graphicsPlacementKey{image: imageID}
	if raw := c.Options['p']; raw != "" && raw != "0" {
		p, err := strconv.ParseUint(raw, 10, 32)
		if err != nil || p == 0 {
			return
		}
		key.placement = uint32(p)
	} else {
		s.nextPlacement++
		if s.nextPlacement == 0 {
			s.nextPlacement++
		}
		key.placement = s.nextPlacement
	}
	p, ok := newGraphicsPlacement(c, imageID, cursor, key.placement)
	if !ok {
		return
	}
	s.placements[key] = p
	if s.placementReply == nil {
		s.placementReply = make(map[graphicsPlacementKey]uint32)
	}
	if raw := c.Options['p']; raw != "" {
		id, _ := strconv.ParseUint(raw, 10, 32)
		s.placementReply[key] = uint32(id)
	}
	s.dirty = true
}

func (s *graphicsViewState) consume(event vt.GraphicsEvent, protocol graphicsProtocol, replies *[][]byte) {
	kitty := protocol == graphicsKitty
	c, err := vt.ParseKittyGraphicsCommand(event.Command, event.Cursor)
	if err != nil || c.ValidateKittyGraphicsRelay() != nil {
		return
	}
	action := c.Action
	if s.uploadID != 0 && action != "" {
		if kitty {
			s.pendingUploads = append(s.pendingUploads, formatGraphicsCommand(map[byte]string{'m': "0", 'q': "2"}, nil))
		}
		s.uploadID, s.uploadInner, s.uploadPlace = 0, 0, nil
		s.assembler.Reset()
	}
	if action == "q" {
		inner, err := strconv.ParseUint(c.Options['i'], 10, 32)
		if err != nil || inner == 0 {
			return
		}
		if !kitty {
			*replies = append(*replies, fmt.Appendf(nil, "\x1b_Gi=%d;ENOTSUP:Kitty graphics unavailable\x1b\\", inner))
			return
		}
		outer := allocOuterGraphicsID()
		if s.responseIDs == nil {
			s.responseIDs = make(map[uint32]uint32)
		}
		s.responseIDs[outer] = uint32(inner)
		opts := cloneGraphicsOptions(c.Options)
		opts['i'] = strconv.FormatUint(uint64(outer), 10)
		s.pendingUploads = append(s.pendingUploads, formatGraphicsCommand(opts, c.Payload))
		return
	}
	if action == "t" || action == "T" {
		inner := uint32(0)
		if raw := c.Options['i']; raw != "" {
			v, err := strconv.ParseUint(raw, 10, 32)
			if err != nil {
				return
			}
			inner = uint32(v)
		}
		if action == "T" || s.uploadID == 0 {
			if prior := s.imageIDs[inner]; prior != 0 {
				if kitty {
					s.pendingUploads = append(s.pendingUploads, deleteImageSequence(prior))
				}
				for key := range s.placements {
					if key.image == prior {
						delete(s.placements, key)
					}
				}
				s.dirty = true
			}
			outer := s.outerID(inner)
			s.uploadID = outer
			s.uploadInner = inner
			s.deleteSixelImage(outer)
			if action == "T" {
				if p, ok := newGraphicsPlacement(c, outer, event.Cursor, 0); ok {
					s.uploadPlace = &p
				} else {
					s.uploadPlace = nil
				}
			}
		}
		if protocol.decodes() {
			if upload, err := s.assembler.Push(c); err == nil && upload != nil {
				if img, err := vt.DecodeKittyImage(*upload); err == nil {
					s.saveSixelImage(s.uploadID, img)
				}
			}
		}
		opts := cloneGraphicsOptions(c.Options)
		if action == "T" {
			opts['a'] = "t"
			for _, key := range []byte{'c', 'r', 'x', 'y', 'w', 'h', 'X', 'Y', 'z', 'C', 'p', 'U'} {
				delete(opts, key)
			}
		}
		opts['i'] = strconv.FormatUint(uint64(s.uploadID), 10)
		if kitty {
			s.pendingUploads = append(s.pendingUploads, formatGraphicsCommand(opts, c.Payload))
		}
		if c.Options['m'] != "1" {
			if s.uploadPlace != nil {
				s.uploadPlace.row, s.uploadPlace.col = event.Cursor.Y, event.Cursor.X
				key := graphicsPlacementKey{image: s.uploadPlace.image, placement: 0}
				s.nextPlacement++
				key.placement = s.nextPlacement
				if raw := s.uploadPlace.options['p']; raw != "" && raw != "0" {
					if id, err := strconv.ParseUint(raw, 10, 32); err == nil && id <= uint64(^uint32(0)) {
						key.placement = uint32(id)
					}
				}
				s.uploadPlace.placement = key.placement
				s.placements[key] = *s.uploadPlace
				if s.placementReply == nil {
					s.placementReply = make(map[graphicsPlacementKey]uint32)
				}
				if raw := s.uploadPlace.options['p']; raw != "" {
					id, _ := strconv.ParseUint(raw, 10, 32)
					s.placementReply[key] = uint32(id)
				}
				s.dirty = true
			}
			s.uploadID, s.uploadInner, s.uploadPlace = 0, 0, nil
		}
		return
	}
	if s.uploadID != 0 && action == "" {
		if c.Options['m'] != "0" && c.Options['m'] != "1" {
			s.uploadID, s.uploadInner, s.uploadPlace = 0, 0, nil
			return
		}
		opts := cloneGraphicsOptions(c.Options)
		opts['m'] = c.Options['m']
		if kitty {
			s.pendingUploads = append(s.pendingUploads, formatGraphicsCommand(opts, c.Payload))
		}
		if protocol.decodes() {
			if upload, err := s.assembler.Push(c); err == nil && upload != nil {
				if img, err := vt.DecodeKittyImage(*upload); err == nil {
					s.saveSixelImage(s.uploadID, img)
				}
			}
		}
		if c.Options['m'] != "1" {
			if s.uploadPlace != nil {
				s.uploadPlace.row, s.uploadPlace.col = event.Cursor.Y, event.Cursor.X
				key := graphicsPlacementKey{image: s.uploadPlace.image}
				s.nextPlacement++
				key.placement = s.nextPlacement
				if raw := s.uploadPlace.options['p']; raw != "" && raw != "0" {
					if id, err := strconv.ParseUint(raw, 10, 32); err == nil && id <= uint64(^uint32(0)) {
						key.placement = uint32(id)
					}
				}
				s.uploadPlace.placement = key.placement
				s.placements[key] = *s.uploadPlace
				if s.placementReply == nil {
					s.placementReply = make(map[graphicsPlacementKey]uint32)
				}
				if raw := s.uploadPlace.options['p']; raw != "" {
					id, _ := strconv.ParseUint(raw, 10, 32)
					s.placementReply[key] = uint32(id)
				}
				s.dirty = true
			}
			s.uploadID, s.uploadPlace = 0, nil
		}
		return
	}
	if action == "p" {
		inner, err := strconv.ParseUint(c.Options['i'], 10, 32)
		if err == nil && s.imageIDs[uint32(inner)] != 0 {
			s.place(c, event.Cursor, uint32(inner))
		} else if err == nil && inner > 0 {
			*replies = append(*replies, fmt.Appendf(nil, "\x1b_Gi=%d;ENOENT:image not uploaded\x1b\\", inner))
		}
		return
	}
	if action == "d" {
		deleting := c.Options['d']
		if deleting == "a" {
			for inner, outer := range s.imageIDs {
				if kitty {
					s.pendingUploads = append(s.pendingUploads, deleteImageSequence(outer))
				}
				delete(s.imageIDs, inner)
				s.deleteSixelImage(outer)
				for key := range s.placements {
					if key.image == outer {
						delete(s.placements, key)
						delete(s.placementReply, key)
					}
				}
			}
			s.dirty = true
			return
		}
		if deleting != "i" && deleting != "p" {
			return // never allow one pane to delete unrelated outer-terminal images
		}
		inner, err := strconv.ParseUint(c.Options['i'], 10, 32)
		if err != nil {
			return
		}
		outer := s.imageIDs[uint32(inner)]
		if outer == 0 {
			return
		}
		if deleting == "p" {
			if raw := c.Options['p']; raw != "" && raw != "0" {
				p, err := strconv.ParseUint(raw, 10, 32)
				if err != nil {
					return
				}
				delete(s.placements, graphicsPlacementKey{image: outer, placement: uint32(p)})
				delete(s.placementReply, graphicsPlacementKey{image: outer, placement: uint32(p)})
			} else {
				for key := range s.placements {
					if key.image == outer {
						delete(s.placements, key)
						delete(s.placementReply, key)
					}
				}
			}
			opts := cloneGraphicsOptions(c.Options)
			opts['i'] = strconv.FormatUint(uint64(outer), 10)
			if kitty {
				s.pendingUploads = append(s.pendingUploads, formatGraphicsCommand(opts, c.Payload))
			}
			s.dirty = true
			return
		}
		opts := cloneGraphicsOptions(c.Options)
		opts['i'] = strconv.FormatUint(uint64(outer), 10)
		if kitty {
			s.pendingUploads = append(s.pendingUploads, formatGraphicsCommand(opts, c.Payload))
		}
		for key := range s.placements {
			if key.image == outer {
				delete(s.placements, key)
				delete(s.placementReply, key)
			}
		}
		delete(s.imageIDs, uint32(inner))
		s.deleteSixelImage(outer)
		s.dirty = true
	}
}

// graphicsPlan places the view's images for one frame. The caller holds
// v.mu: a frame locks every view in scene() and keeps them locked until
// drawScene, so the images match the screen drawn with them.
func (v *view) graphicsPlan(rect layout.Rect, screenX, screenY int, protocol graphicsProtocol, cellW, cellH int, resolved bool) (graphicsPlan, [][]byte) {
	state := &v.graphicsState
	var replies [][]byte
	if !resolved {
		return graphicsPlan{}, nil
	}
	kitty := protocol == graphicsKitty
	if v.graphicsReset {
		if kitty && state.uploadID != 0 {
			state.pendingUploads = append(state.pendingUploads, formatGraphicsCommand(map[byte]string{'m': "0", 'q': "2"}, nil))
		}
		state.placements = nil
		state.placementReply = nil
		state.imageIDs = nil
		state.responseIDs = nil
		state.uploadID, state.uploadInner, state.uploadPlace = 0, 0, nil
		state.dropImages()
		state.auxSequences = nil
		state.auxCells = nil
		state.assembler.Reset()
		state.dirty = true
		state.processed = len(v.graphics)
		state.pendingUploads = nil
		v.graphicsReset = false
	}
	if v.graphicsScrolled {
		state.placements = nil
		state.dirty = true
		v.graphicsScrolled = false
	}
	if state.placements == nil {
		state.placements = make(map[graphicsPlacementKey]graphicsPlacement)
	}
	for state.processed < len(v.graphics) {
		state.consume(v.graphics[state.processed], protocol, &replies)
		state.processed++
	}
	for i := 0; i < state.processed; i++ {
		v.graphicsBytes -= len(v.graphics[i].Command)
		state.mem.queued.give(len(v.graphics[i].Command))
	}
	v.graphics = nil
	state.processed = 0
	plan := graphicsPlan{images: make(map[uint32]struct{}), placements: make(map[graphicsPlacementKey]graphicsPlacement)}
	if kitty {
		plan.uploads = append(plan.uploads, state.pendingUploads...)
		state.pendingUploads = nil
		for _, id := range state.imageIDs {
			plan.images[id] = struct{}{}
		}
		if state.dirty || state.lastRect != rect || state.lastX != screenX || state.lastY != screenY {
			for key, placement := range state.placements {
				if placement.col < 0 || placement.row < 0 || placement.cols <= 0 || placement.rows <= 0 || placement.col+placement.cols > rect.W || placement.row+placement.rows > rect.H {
					continue
				}
				placement.col += screenX
				placement.row += screenY
				plan.placements[key] = placement
			}
			state.dirty = false
			state.lastRect, state.lastX, state.lastY = rect, screenX, screenY
		}
	}
	if (protocol == graphicsSixel || protocol == graphicsIterm) && (state.dirty || state.lastRect != rect || state.lastX != screenX || state.lastY != screenY || state.lastProtocol != protocol) {
		state.auxSequences = nil
		state.auxCells = nil
		keys := make([]graphicsPlacementKey, 0, len(state.placements))
		for key := range state.placements {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].image != keys[j].image {
				return keys[i].image < keys[j].image
			}
			return keys[i].placement < keys[j].placement
		})
		frameBytes := 0
		for _, key := range keys {
			placement := state.placements[key]
			img := state.images[key.image]
			if img == nil || placement.col < 0 || placement.row < 0 || placement.cols <= 0 || placement.rows <= 0 || placement.col+placement.cols > rect.W || placement.row+placement.rows > rect.H {
				continue
			}
			if placement.options['x'] != "" || placement.options['y'] != "" || placement.options['w'] != "" || placement.options['h'] != "" {
				continue
			}
			if protocol == graphicsSixel && (cellW <= 0 || cellH <= 0) {
				continue
			}
			scaled := img
			if protocol == graphicsSixel {
				w, h := placement.cols*cellW, placement.rows*cellH
				if w <= 0 || h <= 0 || int64(w)*int64(h) > vt.MaxSixelPixels {
					continue
				}
				scaled = scaleGraphicsImage(img, w, h)
			}
			var encoded []byte
			var err error
			if protocol == graphicsSixel {
				encoded, err = vt.EncodeSixel(scaled)
			} else {
				var pngData bytes.Buffer
				err = png.Encode(&pngData, scaled)
				if err == nil {
					encoded = fmt.Appendf(nil, "\x1b]1337;File=inline=1;width=%d;height=%d;preserveAspectRatio=0;doNotMoveCursor=1:%s\a", placement.cols, placement.rows, base64.StdEncoding.EncodeToString(pngData.Bytes()))
				}
			}
			limit := maxSixelFrameBytes
			if protocol == graphicsIterm {
				limit = maxItermSequenceBytes
			}
			if err != nil || len(encoded) > limit || len(encoded) > maxSixelFrameBytes-frameBytes {
				if protocol == graphicsIterm || protocol == graphicsSixel {
					state.auxCells = append(state.auxCells, cellGraphicsPlacement{image: img, row: placement.row + screenY, col: placement.col + screenX, rows: placement.rows, cols: placement.cols})
				}
				continue
			}
			frameBytes += len(encoded)
			state.auxSequences = append(state.auxSequences, fmt.Sprintf("\x1b[%d;%dH%s", placement.row+screenY+1, placement.col+screenX+1, encoded))
		}
	}
	switch protocol {
	case graphicsSixel:
		plan.sixels = append(plan.sixels, state.auxSequences...)
		plan.cells = append(plan.cells, state.auxCells...)
	case graphicsIterm:
		plan.iterm = append(plan.iterm, state.auxSequences...)
		plan.cells = append(plan.cells, state.auxCells...)
	case graphicsCells, graphicsText:
		keys := make([]graphicsPlacementKey, 0, len(state.placements))
		for key := range state.placements {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].image != keys[j].image {
				return keys[i].image < keys[j].image
			}
			return keys[i].placement < keys[j].placement
		})
		for _, key := range keys {
			placement := state.placements[key]
			img := state.images[key.image]
			cropped := placement.options['x'] != "" || placement.options['y'] != "" || placement.options['w'] != "" || placement.options['h'] != ""
			if protocol == graphicsText || cropped {
				img = nil // cells draw whole images only
			}
			// An image that cannot be drawn as cells (text mode, cropped,
			// invalid, or past the memory bounds) is still named, as text.
			if placement.col >= 0 && placement.row >= 0 && placement.cols > 0 && placement.rows > 0 && placement.col+placement.cols <= rect.W && placement.row+placement.rows <= rect.H {
				plan.cells = append(plan.cells, cellGraphicsPlacement{image: img, row: placement.row + screenY, col: placement.col + screenX, rows: placement.rows, cols: placement.cols})
			}
		}
	}
	state.dirty = false
	state.lastRect, state.lastX, state.lastY = rect, screenX, screenY
	state.lastProtocol = protocol
	return plan, replies
}

func scaleGraphicsImage(src image.Image, width, height int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	b := src.Bounds()
	for y := 0; y < height; y++ {
		sy := b.Min.Y + y*b.Dy()/height
		for x := 0; x < width; x++ {
			sx := b.Min.X + x*b.Dx()/width
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

// imagePlaceholder names an image drawn as text.
const imagePlaceholder = "[image]"

func applyCellGraphics(scr uv.Screen, placements []cellGraphicsPlacement) {
	bounds := scr.Bounds()
	for _, p := range placements {
		if p.cols <= 0 || p.rows <= 0 {
			continue
		}
		if p.image == nil {
			applyTextGraphics(scr, p)
			continue
		}
		b := p.image.Bounds()
		for y := 0; y < p.rows; y++ {
			for x := 0; x < p.cols; x++ {
				cx, cy := p.col+x, p.row+y
				if cx < bounds.Min.X || cy < bounds.Min.Y || cx >= bounds.Max.X || cy >= bounds.Max.Y {
					continue
				}
				cell := scr.CellAt(cx, cy)
				background := color.Color(color.Black)
				if cell != nil && cell.Style.Bg != nil {
					background = cell.Style.Bg
				}
				sx := b.Min.X + x*b.Dx()/p.cols
				yTop := b.Min.Y + (2*y)*b.Dy()/(2*p.rows)
				yBottom := b.Min.Y + (2*y+1)*b.Dy()/(2*p.rows)
				top := compositeGraphicsPixel(p.image.At(sx, yTop), background)
				bottom := compositeGraphicsPixel(p.image.At(sx, yBottom), background)
				scr.SetCell(cx, cy, &uv.Cell{Content: "▀", Width: 1, Style: uv.Style{Fg: top, Bg: bottom}})
			}
		}
	}
}

// applyTextGraphics names an image in the first row of its placement,
// clipped to its width and the screen; the program's own cells stay.
func applyTextGraphics(scr uv.Screen, p cellGraphicsPlacement) {
	bounds := scr.Bounds()
	if p.row < bounds.Min.Y || p.row >= bounds.Max.Y {
		return
	}
	for i, r := range imagePlaceholder {
		cx := p.col + i
		if i >= p.cols || cx >= bounds.Max.X {
			break
		}
		if cx < bounds.Min.X {
			continue
		}
		scr.SetCell(cx, p.row, &uv.Cell{Content: string(r), Width: 1, Style: uv.Style{Attrs: uv.AttrFaint}})
	}
}

func compositeGraphicsPixel(src, background color.Color) color.RGBA {
	r, g, b, a := src.RGBA()
	br, bg, bb, _ := background.RGBA()
	inv := uint32(0xffff) - a
	return color.RGBA{
		R: uint8((r + uint32((uint64(br)*uint64(inv))/0xffff)) >> 8),
		G: uint8((g + uint32((uint64(bg)*uint64(inv))/0xffff)) >> 8),
		B: uint8((b + uint32((uint64(bb)*uint64(inv))/0xffff)) >> 8),
		A: 0xff,
	}
}

func (v *view) graphicsReply(id uint32, payload []byte, placementID uint32) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	inner, ok := v.graphicsState.responseIDs[id]
	if !ok || inner == 0 {
		return false
	}
	delete(v.graphicsState.responseIDs, id)
	reply := fmt.Sprintf("\x1b_Gi=%d", inner)
	if placementID > 0 {
		innerPlacement := v.graphicsState.placementReply[graphicsPlacementKey{image: id, placement: placementID}]
		if innerPlacement > 0 {
			reply += fmt.Sprintf(",p=%d", innerPlacement)
		}
	}
	reply += ";" + string(payload) + "\x1b\\"
	select {
	case v.ops <- viewOp{data: []byte(reply)}:
	default:
	}
	return true
}

func (a *App) graphicsReply(ev uv.Event) bool {
	response, ok := ev.(uv.KittyGraphicsEvent)
	if !ok || response.Options.ID <= 0 || uint64(response.Options.ID) > uint64(^uint32(0)) || response.Options.PlacementID < 0 || uint64(response.Options.PlacementID) > uint64(^uint32(0)) {
		return false
	}
	for _, b := range response.Payload {
		if b < 0x20 || b > 0x7e {
			return false
		}
	}
	for _, v := range a.views {
		if v.graphicsReply(uint32(response.Options.ID), response.Payload, uint32(max(response.Options.PlacementID, 0))) {
			return true
		}
	}
	return false
}

func (a *App) graphicsTargets(scene *compositor.Scene) []graphicsTarget {
	var targets []graphicsTarget
	if a.ws.snap != nil {
		add := func(p compositor.Pane) {
			if pane := a.ws.snap.pane(p.ID); pane != nil && pane.ProcessID != "" {
				targets = append(targets, graphicsTarget{
					procID: pane.ProcessID, rect: p.Rect,
					x: scene.Regions.Panes.Min.X + p.Rect.X,
					y: scene.Regions.Panes.Min.Y + p.Rect.Y,
				})
			}
		}
		for _, p := range scene.Panes {
			add(p)
		}
		for _, p := range scene.Popups {
			add(p)
		}
	}
	return targets
}

func (a *App) graphicsFramePlan(targets []graphicsTarget, protocol graphicsProtocol, cellW, cellH int, resolved bool) graphicsPlan {
	plan := graphicsPlan{images: make(map[uint32]struct{}), placements: make(map[graphicsPlacementKey]graphicsPlacement)}
	sixelBytes := 0
	for _, target := range targets {
		v := a.views[target.procID]
		if v == nil {
			continue
		}
		part, replies := v.graphicsPlan(target.rect, target.x, target.y, protocol, cellW, cellH, resolved)
		plan.uploads = append(plan.uploads, part.uploads...)
		sequences := part.sixels
		if protocol == graphicsIterm {
			sequences = part.iterm
		}
		for _, sequence := range sequences {
			if len(sequence) > maxSixelStateBytes-sixelBytes {
				continue
			}
			if protocol == graphicsIterm {
				plan.iterm = append(plan.iterm, sequence)
			} else {
				plan.sixels = append(plan.sixels, sequence)
			}
			sixelBytes += len(sequence)
		}
		plan.cells = append(plan.cells, part.cells...)
		for key, placement := range part.placements {
			plan.placements[key] = placement
		}
		for id := range part.images {
			plan.images[id] = struct{}{}
		}
		for _, reply := range replies {
			v.send(reply)
		}
	}
	sort.Strings(plan.sixels)
	sort.Strings(plan.iterm)
	return plan
}

func placementSequence(p graphicsPlacement) string {
	opts := map[byte]string{
		'a': "p", 'i': strconv.FormatUint(uint64(p.image), 10),
		'p': strconv.FormatUint(uint64(p.placement), 10),
		'c': strconv.Itoa(p.cols), 'r': strconv.Itoa(p.rows),
		'C': "1", 'z': "-1",
	}
	for _, key := range []byte{'x', 'y', 'w', 'h'} {
		if value := p.options[key]; value != "" {
			opts[key] = value
		}
	}
	return fmt.Sprintf("\x1b[%d;%dH%s", p.row+1, p.col+1, formatGraphicsCommand(opts, nil))
}

func deletePlacementSequence(key graphicsPlacementKey) string {
	return formatGraphicsCommand(map[byte]string{
		'a': "d", 'd': "p", 'i': strconv.FormatUint(uint64(key.image), 10),
		'p': strconv.FormatUint(uint64(key.placement), 10),
	}, nil)
}

func deleteImageSequence(id uint32) string {
	return formatGraphicsCommand(map[byte]string{'a': "d", 'd': "i", 'i': strconv.FormatUint(uint64(id), 10)}, nil)
}

const (
	graphicsProbeID        = 0x48495645 // "HIVE", unlikely to collide with an application query.
	graphicsProbeTimeout   = 1500 * time.Millisecond
	kittyGraphicsQuery     = "\x1b_Gi=1212763717,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\"
	itermCapabilitiesQuery = "\x1b]1337;Capabilities\x1b\\"
	primaryDA1Query        = "\x1b[c"
)

// graphicsCapabilities describes protocols explicitly confirmed by the outer
// terminal. False remains the safe fallback when a reply is absent or invalid.
type graphicsCapabilities struct {
	Kitty bool
	Sixel bool
	Iterm bool
	CellW int
	CellH int
}

type graphicsProtocol uint8

const (
	graphicsNone graphicsProtocol = iota
	graphicsKitty
	graphicsIterm
	graphicsSixel
	graphicsCells
	// graphicsText draws a placeholder naming each image: for terminals
	// that cannot show coloured cells, or users who want no pixels.
	graphicsText
)

// decodes reports whether the protocol needs images' pixels in the UI
// (converted to Sixel, iTerm2 or cells). Kitty passes uploads through; text
// and none never decode.
func (p graphicsProtocol) decodes() bool {
	return p == graphicsSixel || p == graphicsIterm || p == graphicsCells
}

// Image render modes ([images] render).
const (
	ImageRenderAuto  = "auto"
	ImageRenderCells = "cells"
	ImageRenderText  = "text"
	ImageRenderOff   = "off"
)

// renderProtocol applies the configured render mode to the protocol the
// outer terminal confirmed.
func renderProtocol(mode string, detected graphicsProtocol) graphicsProtocol {
	switch mode {
	case ImageRenderCells:
		return graphicsCells
	case ImageRenderText:
		return graphicsText
	case ImageRenderOff:
		return graphicsNone
	}
	return detected
}

// protocol chooses one output format for a render session: Kitty, confirmed
// iTerm2, confirmed Sixel, then ANSI cell graphics for ordinary terminals.
func (c graphicsCapabilities) protocol() graphicsProtocol {
	switch {
	case c.Kitty:
		return graphicsKitty
	case c.Iterm:
		return graphicsIterm
	case c.Sixel:
		return graphicsSixel
	default:
		return graphicsCells
	}
}

// graphicsProbe consumes only replies correlated with startup capability
// queries. Other terminal events continue through the normal input path.
type graphicsProbe struct {
	active bool
	caps   graphicsCapabilities
}

func (p *graphicsProbe) handle(ev uv.Event) bool {
	if p == nil || !p.active {
		return false
	}

	switch e := ev.(type) {
	case uv.KittyGraphicsEvent:
		if e.Options.ID != graphicsProbeID {
			return false
		}
		p.caps.Kitty = strings.TrimSpace(string(e.Payload)) == "OK"
		return true
	case uv.CellSizeEvent:
		if e.Width > 0 && e.Height > 0 && e.Width <= 256 && e.Height <= 256 {
			p.caps.CellW, p.caps.CellH = e.Width, e.Height
		}
		return true
	case uv.PrimaryDeviceAttributesEvent:
		for _, attr := range e {
			if attr == 4 {
				p.caps.Sixel = true
				break
			}
		}
		return true
	case uv.UnknownOscEvent:
		features, ok := itermCapabilities(e)
		if !ok {
			return false
		}
		// iTerm2 feature reporting uses Sx to advertise Sixel. iTerm2 also
		// supports Kitty graphics, which is confirmed by the Kitty query.
		if hasItermFeature(features, "Sx") {
			p.caps.Sixel = true
		}
		p.caps.Iterm = hasItermFeature(features, "F")
		return true
	default:
		return false
	}
}

// Feature reports concatenate short codes (for example, SxF and 24BIT). Split
// at each uppercase initial so an unrelated feature value cannot look like F.
func hasItermFeature(features, want string) bool {
	for i := 0; i < len(features); {
		start := i
		i++
		for i < len(features) && ((features[i] >= 'a' && features[i] <= 'z') || (features[i] >= '0' && features[i] <= '9')) {
			i++
		}
		if features[start:i] == want {
			return true
		}
	}
	return false
}

func (p *graphicsProbe) finish() {
	if p != nil {
		p.active = false
	}
}

func (p *graphicsProbe) protocol() graphicsProtocol {
	if p == nil {
		return graphicsNone
	}
	return p.caps.protocol()
}

func itermCapabilities(ev uv.UnknownOscEvent) (string, bool) {
	raw := string(ev)
	const prefix = "\x1b]1337;"
	if !strings.HasPrefix(raw, prefix) {
		return "", false
	}

	body := strings.TrimPrefix(raw, prefix)
	body = strings.TrimSuffix(body, "\x07")
	body = strings.TrimSuffix(body, "\x1b\\")
	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, "Capabilities") {
		return "", false
	}
	body = strings.TrimSpace(strings.TrimPrefix(body, "Capabilities"))
	body, ok := strings.CutPrefix(body, "=")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(body), true
}
