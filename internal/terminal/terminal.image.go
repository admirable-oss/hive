package terminal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/vt"
)

// Image transfer limits. Hard limits bound what configuration may allow;
// defaults apply when it sets nothing.
const (
	DefaultMaxImageBytes  = 5 << 20
	MaxImageBytesLimit    = 16 << 20
	DefaultMaxImagePixels = 16 << 20
	MaxImagePixelsLimit   = 64 << 20
	// MaxImageSide matches the graphics assembler's per-side cap.
	MaxImageSide = 16384
	// DefaultImageKeep is how long images delivered as files are kept.
	DefaultImageKeep = time.Hour
	// MaxImageTransfers bounds image buffers held at once by one daemon.
	MaxImageTransfers = 4
	// ImageIdleTimeout ends a transfer whose client stopped sending.
	ImageIdleTimeout = 30 * time.Second
	// maxImageFiles bounds the images kept as files; the oldest go first.
	maxImageFiles = 64
	// recentImageTTL is how long a delivered transfer is remembered, so a
	// retry after a lost reply is not delivered twice.
	recentImageTTL  = 10 * time.Minute
	maxRecentImages = 256
	// kittyChunk is the Kitty protocol's largest base64 payload per command.
	kittyChunk = 4096
)

// ImageInput is how a terminal's program takes a pasted image.
type ImageInput string

const (
	// ImageInputNone: the program takes no images; transfers are refused.
	ImageInputNone ImageInput = ""
	// ImageInputKitty transmits the image on the program's input as Kitty
	// graphics commands (direct data, PNG).
	ImageInputKitty ImageInput = "kitty"
	// ImageInputPath saves the image to a private file and pastes its path,
	// which agents such as Claude Code and Codex attach as an image.
	ImageInputPath ImageInput = "path"
)

// ImageInputs lists the values agent manifests may declare.
var ImageInputs = []ImageInput{ImageInputKitty, ImageInputPath}

// ImageConfig bounds and routes terminal.image transfers. Zero values use
// the defaults.
type ImageConfig struct {
	MaxBytes  int
	MaxPixels int
	// Dir holds images delivered as files; empty turns path delivery off.
	Dir string
	// Keep is how long such files are kept.
	Keep time.Duration
	// Input says how the program in a process takes images; nil refuses all.
	Input func(processID string) ImageInput
}

func (c ImageConfig) withDefaults() ImageConfig {
	if c.MaxBytes <= 0 {
		c.MaxBytes = DefaultMaxImageBytes
	}
	c.MaxBytes = min(c.MaxBytes, MaxImageBytesLimit)
	if c.MaxPixels <= 0 {
		c.MaxPixels = DefaultMaxImagePixels
	}
	c.MaxPixels = min(c.MaxPixels, MaxImagePixelsLimit)
	if c.Keep <= 0 {
		c.Keep = DefaultImageKeep
	}
	return c
}

type imageParams struct {
	ProcessID string `json:"process_id"`
	ViewID    string `json:"view_id,omitempty"`
	// TransferID names the transfer: a retry with the same ID is delivered
	// at most once.
	TransferID string `json:"transfer_id"`
	// Format is the image's encoding; only png.
	Format string `json:"format"`
	// Size is the byte count the client sends on the pipe after it opens.
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
}

// ImageReceipt says how an image reached the program. Done in the open
// result means a retry of a transfer already delivered: send nothing.
type ImageReceipt struct {
	Delivery ImageInput `json:"delivery"`
	Path     string     `json:"path,omitempty"`
	Done     bool       `json:"done,omitempty"`
}

var transferIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// RegisterImages exposes terminal.image: a pipe that carries one image to
// a terminal's program. The image travels as pipe data, never inside a
// request, so it is split into the connection's ordinary chunks and other
// traffic keeps flowing. Nothing reaches the program until every byte has
// arrived and checked out: an interrupted transfer leaves its input as it
// was.
func RegisterImages(r *protocol.Router, svc Service, cfg ImageConfig) {
	im := newImages(svc, cfg)
	im.sweep()
	r.MustRegister("terminal.image", protocol.PipeMethod(im.open))
}

type images struct {
	svc  Service
	cfg  ImageConfig
	busy chan struct{}

	mu       sync.Mutex
	inflight map[string]bool
	recent   map[string]recentImage
	now      func() time.Time
}

type recentImage struct {
	receipt ImageReceipt
	at      time.Time
}

func newImages(svc Service, cfg ImageConfig) *images {
	return &images{
		svc: svc, cfg: cfg.withDefaults(), busy: make(chan struct{}, MaxImageTransfers),
		inflight: map[string]bool{}, recent: map[string]recentImage{}, now: time.Now,
	}
}

func invalidImage(format string, args ...any) error {
	return protocol.NewError(protocol.ErrorCodeInvalidParams, fmt.Errorf(format, args...))
}

func (im *images) open(_ context.Context, p imageParams) (ImageReceipt, protocol.PipeFunc, error) {
	switch {
	case !transferIDPattern.MatchString(p.TransferID):
		return ImageReceipt{}, nil, invalidImage("transfer_id must be 8-64 letters, digits, - or _")
	case p.Format != "png":
		return ImageReceipt{}, nil, invalidImage("format %q is not supported (want png)", p.Format)
	case p.Size <= 0:
		return ImageReceipt{}, nil, invalidImage("size must be positive")
	case p.Size > im.cfg.MaxBytes:
		return ImageReceipt{}, nil, invalidImage("image is %d bytes; the limit is %d", p.Size, im.cfg.MaxBytes)
	}
	sum, err := hex.DecodeString(p.SHA256)
	if err != nil || len(sum) != sha256.Size {
		return ImageReceipt{}, nil, invalidImage("sha256 must be 64 hex digits")
	}
	sess, err := lookup(im.svc, p.ProcessID)
	if err != nil {
		return ImageReceipt{}, nil, err
	}
	input := ImageInputNone
	if im.cfg.Input != nil {
		input = im.cfg.Input(p.ProcessID)
	}
	switch input {
	case ImageInputKitty:
	case ImageInputPath:
		if im.cfg.Dir == "" {
			return ImageReceipt{}, nil, protocol.NewError(protocol.ErrorCodeUnsupported, errors.New("saving pasted images is off on this machine"))
		}
	default:
		return ImageReceipt{}, nil, protocol.NewError(protocol.ErrorCodeUnsupported, errors.New("the program in this pane does not declare image input"))
	}
	if p.ViewID != "" {
		if err := im.svc.Interact(p.ProcessID, p.ViewID); err != nil {
			return ImageReceipt{}, nil, wireError(err)
		}
	}
	key := p.ProcessID + "/" + p.TransferID
	if r, ok := im.delivered(key); ok {
		// The pipe still opens (clients expect one); the client sends nothing.
		return r, func(context.Context, protocol.Pipe) error { return nil }, nil
	}
	return ImageReceipt{Delivery: input}, func(ctx context.Context, pipe protocol.Pipe) error {
		select {
		case im.busy <- struct{}{}:
			defer func() { <-im.busy }()
		default:
			return protocol.NewError(protocol.ErrorCodeUnavailable, errors.New("too many image transfers at once; try again"))
		}
		data, err := receiveImage(ctx, pipe, p.Size)
		if err != nil {
			return err
		}
		if err := im.check(data, sum); err != nil {
			return err
		}
		// Once started, delivery finishes even if the client goes: stopping
		// part-way would leave half an image on the program's input.
		dctx := context.WithoutCancel(ctx)
		receipt, err := im.deliverOnce(key, func() (ImageReceipt, error) {
			return im.deliver(dctx, sess, input, p.TransferID, data)
		})
		if err != nil {
			return err
		}
		receipt.Done = true
		line, _ := json.Marshal(receipt)
		_, err = pipe.Write(append(line, '\n'))
		return err
	}, nil
}

// receiveImage reads exactly size bytes, failing when the client stops
// sending for ImageIdleTimeout or closes early.
func receiveImage(ctx context.Context, pipe protocol.Pipe, size int) ([]byte, error) {
	var stalled atomic.Bool
	idle := time.AfterFunc(ImageIdleTimeout, func() {
		stalled.Store(true)
		if c, ok := pipe.(io.Closer); ok {
			_ = c.Close()
		}
	})
	defer idle.Stop()
	data := make([]byte, size)
	got := 0
	for got < size {
		n, err := pipe.Read(data[got:])
		got += n
		if n > 0 {
			idle.Reset(ImageIdleTimeout)
		}
		if err != nil {
			break
		}
	}
	switch {
	case got == size:
		return data, nil
	case stalled.Load():
		return nil, protocol.NewError(protocol.ErrorCodeTimeout, fmt.Errorf("image transfer stalled after %d of %d bytes", got, size))
	case ctx.Err() != nil:
		return nil, ctx.Err()
	}
	return nil, protocol.NewError(protocol.ErrorCodeUnavailable, fmt.Errorf("image transfer interrupted after %d of %d bytes", got, size))
}

// check validates a received image: its digest, and a PNG header within
// the configured pixel bounds. It does not decode the pixels.
func (im *images) check(data, sum []byte) error {
	if got := sha256.Sum256(data); !bytes.Equal(got[:], sum) {
		return invalidImage("image does not match its sha256")
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return invalidImage("image is not a valid PNG: %v", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > MaxImageSide || cfg.Height > MaxImageSide {
		return invalidImage("image is %dx%d; each side must be 1..%d", cfg.Width, cfg.Height, MaxImageSide)
	}
	if px := int64(cfg.Width) * int64(cfg.Height); px > int64(im.cfg.MaxPixels) {
		return invalidImage("image has %d pixels; the limit is %d", px, im.cfg.MaxPixels)
	}
	return nil
}

func (im *images) delivered(key string) (ImageReceipt, bool) {
	im.mu.Lock()
	defer im.mu.Unlock()
	r, ok := im.recent[key]
	if !ok || im.now().Sub(r.at) > recentImageTTL {
		return ImageReceipt{}, false
	}
	r.receipt.Done = true
	return r.receipt, true
}

// deliverOnce runs deliver unless the transfer was already delivered or is
// being delivered by an earlier attempt (whose client may have gone).
func (im *images) deliverOnce(key string, deliver func() (ImageReceipt, error)) (ImageReceipt, error) {
	if r, ok := im.delivered(key); ok {
		return r, nil
	}
	im.mu.Lock()
	if im.inflight[key] {
		im.mu.Unlock()
		return ImageReceipt{}, protocol.NewError(protocol.ErrorCodeUnavailable, errors.New("this image is still being delivered; try again"))
	}
	im.inflight[key] = true
	im.mu.Unlock()

	r, err := deliver()

	im.mu.Lock()
	defer im.mu.Unlock()
	delete(im.inflight, key)
	if err == nil {
		now := im.now()
		for k, v := range im.recent {
			if now.Sub(v.at) > recentImageTTL {
				delete(im.recent, k)
			}
		}
		if len(im.recent) >= maxRecentImages {
			oldest := ""
			for k, v := range im.recent {
				if oldest == "" || v.at.Before(im.recent[oldest].at) {
					oldest = k
				}
			}
			delete(im.recent, oldest)
		}
		im.recent[key] = recentImage{receipt: r, at: now}
	}
	return r, err
}

func (im *images) deliver(ctx context.Context, sess Session, input ImageInput, transferID string, data []byte) (ImageReceipt, error) {
	switch input {
	case ImageInputKitty:
		return ImageReceipt{Delivery: input}, writeKitty(sess, transferID, data)
	case ImageInputPath:
		path, err := im.save(transferID, data)
		if err != nil {
			return ImageReceipt{}, err
		}
		if err := pastePath(ctx, sess, path); err != nil {
			_ = os.Remove(path)
			return ImageReceipt{}, err
		}
		return ImageReceipt{Delivery: input, Path: path}, nil
	}
	return ImageReceipt{}, protocol.NewError(protocol.ErrorCodeUnsupported, fmt.Errorf("image input %q", input))
}

// writeKitty types data into sess as Kitty direct transmissions, one
// complete APC command per write, so keystrokes from other views can only
// fall between commands. q=2 keeps the program from answering on its
// output. If a write fails after the first, a final empty chunk ends the
// upload so the program does not wait for more.
func writeKitty(sess Session, transferID string, data []byte) error {
	for i, cmd := range KittyTransmit(kittyImageID(transferID), data) {
		if _, err := sess.Write(cmd); err != nil {
			if i > 0 {
				_, _ = sess.Write([]byte("\x1b_Gm=0,q=2;\x1b\\"))
			}
			return err
		}
	}
	return nil
}

// KittyTransmit encodes PNG data as Kitty direct-transmission commands.
// Only the first carries the full header; continuations carry m and q.
func KittyTransmit(id uint32, data []byte) [][]byte {
	encoded := base64.StdEncoding.EncodeToString(data)
	var out [][]byte
	for offset := 0; offset < len(encoded); offset += kittyChunk {
		end := min(offset+kittyChunk, len(encoded))
		more := 0
		if end < len(encoded) {
			more = 1
		}
		var head string
		if offset == 0 {
			head = fmt.Sprintf("a=t,t=d,f=100,i=%d,q=2,m=%d", id, more)
		} else {
			head = fmt.Sprintf("m=%d,q=2", more)
		}
		out = append(out, []byte("\x1b_G"+head+";"+encoded[offset:end]+"\x1b\\"))
	}
	return out
}

// kittyImageID derives a stable, non-zero 31-bit image ID from a transfer.
func kittyImageID(transferID string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(transferID))
	if id := h.Sum32() & 0x7fffffff; id != 0 {
		return id
	}
	return 1
}

// pastePath types path into sess, bracketed when the program asked for
// bracketed paste, so agents read it as one pasted path.
func pastePath(ctx context.Context, sess Session, path string) error {
	text := []byte(path)
	if s, err := sess.Snapshot(ctx); err == nil && s.Modes&vt.ModeBracketedPaste != 0 {
		text = slices.Concat([]byte("\x1b[200~"), text, []byte("\x1b[201~"))
	}
	_, err := sess.Write(text)
	return err
}

// save writes data to a private file in the image directory and removes
// expired or excess ones.
func (im *images) save(transferID string, data []byte) (string, error) {
	if err := privateDir(im.cfg.Dir); err != nil {
		return "", err
	}
	path := filepath.Join(im.cfg.Dir, "hive-image-"+transferID+".png")
	tmp, err := os.CreateTemp(im.cfg.Dir, ".tmp-*")
	if err != nil {
		return "", err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	im.sweep()
	return path, nil
}

// privateDir makes dir (0700) and refuses one that is a symlink, belongs to
// someone else or is open to others.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := platform.CheckPrivateDir(dir); err != nil {
		return fmt.Errorf("image directory: %w", err)
	}
	return nil
}

// sweep removes saved images past their keep time, then the oldest beyond
// maxImageFiles, and abandoned temporary files.
func (im *images) sweep() {
	if im.cfg.Dir == "" {
		return
	}
	entries, err := os.ReadDir(im.cfg.Dir)
	if err != nil {
		return
	}
	type file struct {
		path string
		mod  time.Time
	}
	var kept []file
	now := im.now()
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(im.cfg.Dir, e.Name())
		if now.Sub(info.ModTime()) > im.cfg.Keep {
			_ = os.Remove(path)
			continue
		}
		kept = append(kept, file{path, info.ModTime()})
	}
	slices.SortFunc(kept, func(a, b file) int { return a.mod.Compare(b.mod) })
	for len(kept) > maxImageFiles {
		_ = os.Remove(kept[0].path)
		kept = kept[1:]
	}
}
