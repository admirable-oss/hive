package client

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/admirable-oss/hive/internal/protocol"
)

// ImageRequest sends one PNG image to a terminal's program
// (terminal.image).
type ImageRequest struct {
	ProcessID string
	ViewID    string
	// TransferID names the transfer; attempts with the same ID deliver the
	// image at most once. Empty: TerminalImage makes one.
	TransferID string
	PNG        []byte
}

// ImageResult says how the image reached the program: delivery is kitty
// (graphics commands on its input) or path (saved to Path, which was
// pasted).
type ImageResult struct {
	Delivery string `json:"delivery"`
	Path     string `json:"path,omitempty"`
	Done     bool   `json:"done,omitempty"`
}

// ErrImageUnsupported is returned by daemons from before image transfer.
var ErrImageUnsupported = errors.New("that machine's hive daemon cannot receive images; update hive there (connecting with --machine offers to) and restart its daemon")

// imageAttempts and imageBackoff bound retries of an interrupted transfer.
const imageAttempts = 3

var imageBackoff = []time.Duration{500 * time.Millisecond, 2 * time.Second}

type imageParams struct {
	ProcessID  string `json:"process_id"`
	ViewID     string `json:"view_id,omitempty"`
	TransferID string `json:"transfer_id"`
	Format     string `json:"format"`
	Size       int    `json:"size"`
	SHA256     string `json:"sha256"`
}

func (s *service) TerminalImage(ctx context.Context, req ImageRequest) (ImageResult, error) {
	if req.TransferID == "" {
		req.TransferID = newTransferID()
	}
	sum := sha256.Sum256(req.PNG)
	params := imageParams{
		ProcessID: req.ProcessID, ViewID: req.ViewID, TransferID: req.TransferID,
		Format: "png", Size: len(req.PNG), SHA256: hex.EncodeToString(sum[:]),
	}
	var err error
	for attempt := range imageAttempts {
		if attempt > 0 {
			t := time.NewTimer(imageBackoff[min(attempt-1, len(imageBackoff)-1)])
			select {
			case <-ctx.Done():
				t.Stop()
				return ImageResult{}, err
			case <-t.C:
			}
		}
		var res ImageResult
		res, err = s.sendImage(ctx, params, req.PNG)
		if err == nil || !transientImageError(err) || ctx.Err() != nil {
			return res, err
		}
	}
	return ImageResult{}, err
}

// sendImage makes one attempt: open the pipe, send the bytes (paced on a
// remote link), and read the daemon's receipt.
func (s *service) sendImage(ctx context.Context, params imageParams, data []byte) (ImageResult, error) {
	mux, err := s.conn(ctx)
	if errors.Is(err, errLegacy) {
		return ImageResult{}, ErrDaemonTooOld
	}
	if err != nil {
		return ImageResult{}, err
	}
	var opened ImageResult
	p, err := mux.OpenPipe(ctx, "terminal.image", params, &opened)
	if pe, ok := errors.AsType[*protocol.Error](err); ok && pe.Code == protocol.ErrorCodeUnknownMethod {
		return ImageResult{}, ErrImageUnsupported
	}
	if err != nil {
		return ImageResult{}, fmt.Errorf("terminal.image: %w", err)
	}
	defer p.Close()
	stop := context.AfterFunc(ctx, func() { _ = p.Close() })
	defer stop()
	if opened.Done {
		return opened, nil // an earlier attempt delivered it
	}
	budget := 0
	if s.config.Remote {
		budget = s.config.ImageBudget
	}
	start := time.Now()
	for sent := 0; sent < len(data); {
		n := min(len(data)-sent, protocol.PipeChunk)
		if _, err := p.Write(data[sent : sent+n]); err != nil {
			break // the daemon closed the pipe; its reason is read below
		}
		sent += n
		if budget > 0 {
			ahead := time.Duration(float64(sent)/float64(budget)*float64(time.Second)) - time.Since(start)
			if ahead > 0 {
				t := time.NewTimer(ahead)
				select {
				case <-ctx.Done():
					t.Stop()
					return ImageResult{}, ctx.Err()
				case <-t.C:
				}
			}
		}
	}
	line, err := bufio.NewReader(io.LimitReader(p, 4096)).ReadBytes('\n')
	if ctx.Err() != nil {
		return ImageResult{}, ctx.Err()
	}
	if err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF // closed without a receipt
		}
		return ImageResult{}, fmt.Errorf("terminal.image: %w", err)
	}
	var res ImageResult
	if err := json.Unmarshal(line, &res); err != nil {
		return ImageResult{}, fmt.Errorf("terminal.image: bad receipt: %w", err)
	}
	return res, nil
}

// transientImageError reports whether a failed attempt may succeed when
// retried with the same transfer ID: the link broke or the daemon was busy.
// Refusals (too large, not a PNG, no image input) are final.
func transientImageError(err error) bool {
	if pe, ok := errors.AsType[*protocol.Error](err); ok {
		return pe.Code == protocol.ErrorCodeUnavailable || pe.Code == protocol.ErrorCodeTimeout
	}
	var ne net.Error
	return errors.Is(err, protocol.ErrConnClosed) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, io.ErrClosedPipe) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) || errors.As(err, &ne)
}

func newTransferID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
