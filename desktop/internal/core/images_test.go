package core

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestImagesReachThePane: an image a program prints (Kitty graphics)
// arrives as a placement with a PNG Hive encoded, at the cursor's cell.
func TestImagesReachThePane(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a daemon")
	}
	if runtime.GOOS == "windows" {
		t.Skip("types POSIX shell commands")
	}
	rec := &recorder{}
	h := New(Config{Home: testHome(t), Hive: hiveBin, Emitter: rec})
	h.Start()
	defer h.Close()
	waitFor(t, "a connection", 30*time.Second, func() bool { return h.Connection().State == StateConnected })
	if err := h.CreateEnvironment("pics", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	pane, err := h.NewTab("pics")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the pane", 10*time.Second, func() bool { _, ok := paneByID(h.Workspace(), pane); return ok })
	if err := h.OpenView("w", pane, 80, 24); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the shell's prompt", 15*time.Second, func() bool { return strings.TrimSpace(rec.screen(pane)) != "" })

	var b bytes.Buffer
	_ = png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 6, 3)))
	data := base64.StdEncoding.EncodeToString(b.Bytes())
	if err := h.Input(pane, `printf '\033_Ga=T,f=100,i=7,c=4,r=2;`+data+`\033\\'`+"\r"); err != nil {
		t.Fatal(err)
	}
	var got ImageUpdate
	waitFor(t, "the image", 15*time.Second, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		for _, e := range rec.events {
			if u, ok := e.data.(ImageUpdate); ok && u.Op == "place" && u.Pane == pane {
				got = u
				return true
			}
		}
		return false
	})
	if got.Image != 7 || got.Cols != 4 || got.Rows != 2 || !strings.HasPrefix(got.Src, "data:image/png;base64,") {
		t.Fatalf("placement %+v", got)
	}
}
