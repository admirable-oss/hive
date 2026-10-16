package terminal_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/terminal"
)

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

type imageRig struct {
	fake *fakeSession
	c    *protocol.MuxClient
	dir  string
}

// newImageRig serves terminal.image for one fake session, proc-img, whose
// program takes images as input declares.
func newImageRig(t *testing.T, input terminal.ImageInput, cfg terminal.ImageConfig) *imageRig {
	t.Helper()
	fake := newFakeSession(200)
	svc := newTestService(t, &fakeFactory{openFn: func(context.Context, terminal.Command) (terminal.Session, error) { return fake, nil }})
	if _, err := svc.Open(context.Background(), "proc-img", terminal.Command{Path: "/bin/sh"}); err != nil {
		t.Fatal(err)
	}
	if cfg.Dir == "" {
		cfg.Dir = filepath.Join(t.TempDir(), "images")
	}
	cfg.Input = func(id string) terminal.ImageInput {
		if id == "proc-img" {
			return input
		}
		return terminal.ImageInputNone
	}
	r := protocol.NewRouter()
	terminal.RegisterImages(r, svc, cfg)
	server, client := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- protocol.ServeConn(context.Background(), server, r, protocol.ServerInfo{Version: "test"})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := protocol.Handshake(ctx, client, protocol.Hello{Client: "test"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Close()
		<-done
	})
	return &imageRig{fake: fake, c: c, dir: cfg.Dir}
}

type imageReq struct {
	ProcessID  string `json:"process_id"`
	TransferID string `json:"transfer_id"`
	Format     string `json:"format"`
	Size       int    `json:"size"`
	SHA256     string `json:"sha256"`
}

func reqFor(data []byte, transfer string) imageReq {
	sum := sha256.Sum256(data)
	return imageReq{ProcessID: "proc-img", TransferID: transfer, Format: "png", Size: len(data), SHA256: hex.EncodeToString(sum[:])}
}

// send opens terminal.image, writes body and returns the receipt.
func (r *imageRig) send(req imageReq, body []byte) (opened, final terminal.ImageReceipt, err error) {
	p, err := r.c.OpenPipe(context.Background(), "terminal.image", req, &opened)
	if err != nil {
		return opened, final, err
	}
	defer p.Close()
	if opened.Done {
		return opened, opened, nil
	}
	_, _ = p.Write(body)
	out, err := io.ReadAll(p)
	if err != nil {
		return opened, final, err
	}
	err = json.Unmarshal(out, &final)
	return opened, final, err
}

func (r *imageRig) typed() string {
	r.fake.mu.Lock()
	defer r.fake.mu.Unlock()
	return r.fake.buf.String()
}

func errCode(err error) string {
	if pe, ok := errors.AsType[*protocol.Error](err); ok {
		return pe.Code
	}
	return ""
}

var apc = regexp.MustCompile("\x1b_G([^;]*);([^\x1b]*)\x1b\\\\")

func TestImage_KittyDeliveryIsChunkedAndComplete(t *testing.T) {
	r := newImageRig(t, terminal.ImageInputKitty, terminal.ImageConfig{})
	data := pngOf(t, 200, 150)
	data = append(data, make([]byte, 10_000)...) // trailing bytes after IEND: still one PNG, many chunks
	_, receipt, err := r.send(reqFor(data, "kitty-transfer-1"), data)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Delivery != terminal.ImageInputKitty || !receipt.Done {
		t.Fatalf("receipt = %+v", receipt)
	}
	cmds := apc.FindAllStringSubmatch(r.typed(), -1)
	if len(cmds) < 3 {
		t.Fatalf("got %d commands, want several chunks", len(cmds))
	}
	var payload strings.Builder
	for i, m := range cmds {
		head, body := m[1], m[2]
		last := i == len(cmds)-1
		switch {
		case i == 0 && !strings.HasPrefix(head, "a=t,t=d,f=100,i="):
			t.Fatalf("first header %q", head)
		case i > 0 && head != "m=1,q=2" && head != "m=0,q=2":
			t.Fatalf("continuation header %q carries more than m and q", head)
		case last != strings.Contains(head, "m=0"):
			t.Fatalf("chunk %d header %q: only the last has m=0", i, head)
		case len(body) > 4096:
			t.Fatalf("chunk %d is %d bytes", i, len(body))
		}
		payload.WriteString(body)
	}
	got, err := base64.StdEncoding.DecodeString(payload.String())
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("reassembled payload differs (%v)", err)
	}
}

func TestImage_PathDeliverySavesPrivateFileAndPastesPath(t *testing.T) {
	r := newImageRig(t, terminal.ImageInputPath, terminal.ImageConfig{})
	data := pngOf(t, 4, 4)
	_, receipt, err := r.send(reqFor(data, "path-transfer-1"), data)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Delivery != terminal.ImageInputPath || filepath.Dir(receipt.Path) != r.dir {
		t.Fatalf("receipt = %+v", receipt)
	}
	saved, err := os.ReadFile(receipt.Path)
	if err != nil || !bytes.Equal(saved, data) {
		t.Fatalf("saved file: %v", err)
	}
	for _, p := range []string{r.dir, receipt.Path} {
		fi, err := os.Stat(p)
		if err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0) {
			t.Fatalf("%s is open to others: %v %v", p, fi.Mode(), err)
		}
	}
	if r.typed() != receipt.Path {
		t.Fatalf("typed %q, want the path", r.typed())
	}
}

func TestImage_InterruptedTransferDeliversNothing(t *testing.T) {
	for _, input := range []terminal.ImageInput{terminal.ImageInputKitty, terminal.ImageInputPath} {
		t.Run(string(input), func(t *testing.T) {
			r := newImageRig(t, input, terminal.ImageConfig{})
			data := pngOf(t, 300, 300)
			data = append(data, bytes.Repeat([]byte{1}, 100_000)...)
			p, err := r.c.OpenPipe(context.Background(), "terminal.image", reqFor(data, "cut-transfer-1"), nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.Write(data[:len(data)/2]); err != nil {
				t.Fatal(err)
			}
			_ = p.Close() // the link drops mid-transfer
			time.Sleep(100 * time.Millisecond)
			if got := r.typed(); got != "" {
				t.Fatalf("a partial transfer reached the program: %d bytes", len(got))
			}
			if entries, _ := os.ReadDir(r.dir); len(entries) != 0 {
				t.Fatalf("a partial transfer left files: %v", entries)
			}
			// The retry delivers it whole.
			if _, _, err := r.send(reqFor(data, "cut-transfer-1"), data); err != nil {
				t.Fatal(err)
			}
			if r.typed() == "" {
				t.Fatal("the retry delivered nothing")
			}
		})
	}
}

func TestImage_RetryOfDeliveredTransferIsNotDeliveredTwice(t *testing.T) {
	r := newImageRig(t, terminal.ImageInputPath, terminal.ImageConfig{})
	data := pngOf(t, 8, 8)
	_, first, err := r.send(reqFor(data, "dup-transfer-1"), data)
	if err != nil {
		t.Fatal(err)
	}
	opened, again, err := r.send(reqFor(data, "dup-transfer-1"), data)
	if err != nil {
		t.Fatal(err)
	}
	if !opened.Done || again.Path != first.Path {
		t.Fatalf("retry: opened %+v, receipt %+v; want the first delivery (%+v)", opened, again, first)
	}
	if r.typed() != first.Path {
		t.Fatalf("typed %q: the image was delivered twice", r.typed())
	}
}

func TestImage_RejectsBadTransfers(t *testing.T) {
	good := pngOf(t, 10, 10)
	big := pngOf(t, 100, 100)
	corrupt := append([]byte(nil), good...)
	corrupt[len(corrupt)/2] ^= 0xff
	notPNG := []byte("GIF89a this is not a png at all")
	cases := []struct {
		name   string
		input  terminal.ImageInput
		cfg    terminal.ImageConfig
		req    imageReq
		body   []byte
		code   string
		atOpen bool
	}{
		{name: "declared too large", input: terminal.ImageInputKitty, cfg: terminal.ImageConfig{MaxBytes: 64}, req: reqFor(good, "too-large-1"), body: good, code: protocol.ErrorCodeInvalidParams, atOpen: true},
		{name: "unsupported format", input: terminal.ImageInputKitty, req: func() imageReq { q := reqFor(good, "bad-format-1"); q.Format = "gif"; return q }(), code: protocol.ErrorCodeInvalidParams, atOpen: true},
		{name: "bad transfer id", input: terminal.ImageInputKitty, req: reqFor(good, "x"), code: protocol.ErrorCodeInvalidParams, atOpen: true},
		{name: "program takes no images", input: terminal.ImageInputNone, req: reqFor(good, "no-input-1"), code: protocol.ErrorCodeUnsupported, atOpen: true},
		{name: "digest mismatch", input: terminal.ImageInputKitty, req: reqFor(good, "digest-1"), body: corrupt, code: protocol.ErrorCodeInvalidParams},
		{name: "not a png", input: terminal.ImageInputKitty, req: reqFor(notPNG, "not-png-1"), body: notPNG, code: protocol.ErrorCodeInvalidParams},
		{name: "too many pixels", input: terminal.ImageInputKitty, cfg: terminal.ImageConfig{MaxPixels: 50 * 50}, req: reqFor(big, "pixels-1"), body: big, code: protocol.ErrorCodeInvalidParams},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newImageRig(t, tc.input, tc.cfg)
			p, err := r.c.OpenPipe(context.Background(), "terminal.image", tc.req, nil)
			if tc.atOpen {
				if errCode(err) != tc.code {
					t.Fatalf("open: %v, want %s", err, tc.code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			_, _ = p.Write(tc.body)
			_, err = io.ReadAll(p)
			if errCode(err) != tc.code {
				t.Fatalf("got %v, want %s", err, tc.code)
			}
			if r.typed() != "" {
				t.Fatal("a rejected image reached the program")
			}
		})
	}
}

func TestKittyTransmit_SingleChunk(t *testing.T) {
	cmds := terminal.KittyTransmit(7, []byte("png"))
	if len(cmds) != 1 || string(cmds[0]) != "\x1b_Ga=t,t=d,f=100,i=7,q=2,m=0;cG5n\x1b\\" {
		t.Fatalf("got %q", cmds)
	}
}
