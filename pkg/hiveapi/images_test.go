package hiveapi

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func ev(cmd string, x, y int) GraphicsEvent {
	return GraphicsEvent{Command: []byte("\x1b_G" + cmd + "\x1b\\"), Cursor: Cursor{X: x, Y: y}}
}

func TestKittyImages(t *testing.T) {
	var k KittyImages
	data := base64.StdEncoding.EncodeToString(pngOf(t, 8, 4))

	// Transmit and show, in two chunks.
	half := len(data) / 2 / 4 * 4
	if ops := k.Push(ev("a=T,f=100,i=7,c=4,r=2,m=1;"+data[:half], 3, 5)); ops != nil {
		t.Fatalf("a first chunk shows something: %+v", ops)
	}
	ops := k.Push(ev("m=0;"+data[half:], 9, 9))
	if len(ops) != 2 || ops[0].Kind != ImageDelete || ops[1].Kind != ImagePlace {
		t.Fatalf("ops %+v", ops)
	}
	p := ops[1]
	if p.Image != 7 || p.X != 3 || p.Y != 5 || p.Cols != 4 || p.Rows != 2 {
		t.Fatalf("placement %+v", p)
	}
	if img, err := png.Decode(bytes.NewReader(p.PNG)); err != nil || img.Bounds().Dx() != 8 {
		t.Fatalf("the image is not a PNG of it: %v", err)
	}

	// Transmit only, then place twice.
	k.Push(ev("a=t,f=100,i=9;"+data, 0, 0))
	if ops := k.Push(ev("a=p,i=9,c=2,r=1;", 1, 1)); len(ops) != 1 || ops[0].Kind != ImagePlace || len(ops[0].PNG) == 0 {
		t.Fatalf("placing a stored image: %+v", ops)
	}
	if ops := k.Push(ev("a=p,i=9;", 1, 1)); ops != nil {
		t.Fatalf("a placement without a size in cells: %+v", ops)
	}
	if ops := k.Push(ev("a=p,i=404,c=2,r=1;", 1, 1)); ops != nil {
		t.Fatalf("placing an unknown image: %+v", ops)
	}

	// Queries get an answer; deletions.
	if ops := k.Push(ev("a=q,i=31,s=1,v=1,f=24;AAAA", 0, 0)); len(ops) != 1 || string(ops[0].Reply) != "\x1b_Gi=31;OK\x1b\\" {
		t.Fatalf("query: %+v", ops)
	}
	if ops := k.Push(ev("a=d,d=I,i=9;", 0, 0)); len(ops) != 1 || ops[0].Kind != ImageDelete || ops[0].Image != 9 {
		t.Fatalf("delete: %+v", ops)
	}
	if ops := k.Push(ev("a=p,i=9,c=2,r=1;", 1, 1)); ops != nil {
		t.Fatal("a freed image is placed")
	}
	if ops := k.Push(ev("a=d,d=a;", 0, 0)); len(ops) != 1 || ops[0].Kind != ImageClear {
		t.Fatalf("delete all: %+v", ops)
	}

	// Garbage and lies are dropped: a bad payload, an oversized image.
	if ops := k.Push(ev("a=T,f=100,i=3,c=1,r=1;not base64!", 0, 0)); ops != nil {
		t.Fatalf("garbage: %+v", ops)
	}
	if ops := k.Push(ev("a=T,f=24,s=100000,v=100000,i=4,c=1,r=1;AAAA", 0, 0)); ops != nil {
		t.Fatalf("an impossible image: %+v", ops)
	}
	// An interrupted upload never mixes with the next.
	k.Push(ev("a=T,f=100,i=5,c=1,r=1,m=1;"+data[:half], 0, 0))
	if ops := k.Push(ev("a=d,d=a;", 0, 0)); len(ops) != 1 {
		t.Fatalf("a command during an upload: %+v", ops)
	}
	if ops := k.Push(ev("m=0;"+data[half:], 0, 0)); ops != nil {
		t.Fatalf("the rest of an abandoned upload shows: %+v", ops)
	}
}
