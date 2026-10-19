package hiveapi

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"strconv"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/vt"
)

// Images agents print (Kitty graphics, M11), for programs that draw
// terminals themselves.
type (
	// GraphicsStream yields a terminal's Kitty graphics commands, each
	// with the cursor cell it arrived at.
	GraphicsStream = client.GraphicsStream
	GraphicsEvent  = vt.GraphicsEvent
)

// Graphics opens a terminal's graphics stream (see KittyImages).
func (c *Client) Graphics(ctx context.Context, processID string, width, height int) (*GraphicsStream, error) {
	return c.c.TerminalGraphics(ctx, client.ViewRequest{ProcessID: processID, Width: uint16(width), Height: uint16(height)})
}

// What an ImageOp does.
const (
	// ImagePlace shows an image at a cell, sized in cells.
	ImagePlace = "place"
	// ImageDelete removes every placement of one image.
	ImageDelete = "delete"
	// ImageClear removes every image.
	ImageClear = "clear"
	// ImageReply is an answer the terminal owes the program (to a support
	// query): write Reply to it as input.
	ImageReply = "reply"
)

// ImageOp is one change to what a terminal shows.
type ImageOp struct {
	Kind      string
	Image     uint32
	Placement uint32
	// X, Y, Cols and Rows place it, in cells of the screen.
	X, Y, Cols, Rows int
	// PNG is the image, decoded and encoded again: nothing the program
	// sent reaches a viewer's image decoder as it was.
	PNG   []byte
	Reply []byte
}

// Limits on what KittyImages keeps.
const (
	maxKeptImages     = 64
	maxKeptImageBytes = 64 << 20
)

// KittyImages turns a terminal's Kitty graphics commands into images to
// show: it assembles chunked uploads, validates and decodes them (the same
// bounds as the daemon's), and keeps uploaded images for later placements.
// Placements need a size in cells (c and r), as Hive's multiplexer
// requires. Its zero value is ready; it is not safe for concurrent use.
type KittyImages struct {
	assembler vt.KittyGraphicsAssembler
	uploading bool
	uploadID  uint32
	place     *ImageOp
	images    map[uint32][]byte
	order     []uint32
	bytes     int
	next      uint32
}

func parseID(s string) (uint32, bool) {
	if s == "" {
		return 0, true
	}
	v, err := strconv.ParseUint(s, 10, 32)
	return uint32(v), err == nil
}

func placement(c vt.KittyGraphicsCommand, image uint32, cursor vt.Cursor) (ImageOp, bool) {
	cols, err1 := strconv.Atoi(c.Options['c'])
	rows, err2 := strconv.Atoi(c.Options['r'])
	if err1 != nil || err2 != nil || cols <= 0 || rows <= 0 || cols > vt.MaxCols || rows > vt.MaxRows {
		return ImageOp{}, false
	}
	p, _ := parseID(c.Options['p'])
	return ImageOp{Kind: ImagePlace, Image: image, Placement: p, X: cursor.X, Y: cursor.Y, Cols: cols, Rows: rows}, true
}

// Push takes one graphics event and returns what changes.
func (k *KittyImages) Push(ev GraphicsEvent) []ImageOp {
	c, err := vt.ParseKittyGraphicsCommand(ev.Command, ev.Cursor)
	if err != nil || c.ValidateKittyGraphicsRelay() != nil {
		return nil
	}
	if k.uploading && c.Action != "" {
		// A new command before the last chunk: the upload is abandoned.
		k.uploading, k.place = false, nil
		k.assembler.Reset()
	}
	switch c.Action {
	case "q":
		id, ok := parseID(c.Options['i'])
		if !ok || id == 0 {
			return nil
		}
		return []ImageOp{{Kind: ImageReply, Reply: fmt.Appendf(nil, "\x1b_Gi=%d;OK\x1b\\", id)}}
	case "t", "T", "":
		if c.Action == "" && !k.uploading {
			return nil
		}
		if c.Action != "" {
			id, ok := parseID(c.Options['i'])
			if !ok {
				return nil
			}
			if id == 0 {
				k.next++
				id = 1<<31 | k.next // an anonymous image: an ID of its own
			}
			k.uploading, k.uploadID, k.place = true, id, nil
			if c.Action == "T" {
				if p, ok := placement(c, id, ev.Cursor); ok {
					k.place = &p
				}
			}
		}
		upload, err := k.assembler.Push(c)
		if err != nil {
			k.uploading, k.place = false, nil
			return nil
		}
		if upload == nil {
			return nil // more chunks to come
		}
		k.uploading = false
		img, err := vt.DecodeKittyImage(*upload)
		if err != nil {
			k.place = nil
			return nil
		}
		var b bytes.Buffer
		if err := png.Encode(&b, img); err != nil {
			return nil
		}
		ops := []ImageOp{{Kind: ImageDelete, Image: k.uploadID}} // replaces the image's old placements
		k.keep(k.uploadID, b.Bytes())
		if k.place != nil {
			p := *k.place
			p.PNG = b.Bytes()
			ops = append(ops, p)
			k.place = nil
		}
		return ops
	case "p":
		id, ok := parseID(c.Options['i'])
		data := k.images[id]
		if !ok || data == nil {
			return nil
		}
		p, ok := placement(c, id, ev.Cursor)
		if !ok {
			return nil
		}
		p.PNG = data
		return []ImageOp{p}
	case "d":
		switch c.Options['d'] {
		case "i", "I":
			if id, ok := parseID(c.Options['i']); ok && id != 0 {
				if c.Options['d'] == "I" {
					k.forget(id)
				}
				return []ImageOp{{Kind: ImageDelete, Image: id}}
			}
			return nil
		default:
			// a/A (all), or any narrower deletion this does not follow:
			// clearing is never wrong to show.
			return []ImageOp{{Kind: ImageClear}}
		}
	}
	return nil
}

func (k *KittyImages) keep(id uint32, data []byte) {
	if k.images == nil {
		k.images = map[uint32][]byte{}
	}
	k.forget(id)
	k.images[id] = data
	k.order = append(k.order, id)
	k.bytes += len(data)
	for (len(k.order) > maxKeptImages || k.bytes > maxKeptImageBytes) && len(k.order) > 1 {
		k.forget(k.order[0])
	}
}

func (k *KittyImages) forget(id uint32) {
	data, ok := k.images[id]
	if !ok {
		return
	}
	delete(k.images, id)
	k.bytes -= len(data)
	for i, x := range k.order {
		if x == id {
			k.order = append(k.order[:i], k.order[i+1:]...)
			break
		}
	}
}
