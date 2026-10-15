package vt

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"

	// Register PNG decoding for image.Decode in Kitty image validation.
	_ "image/png"
	"io"
	"strconv"
	"strings"
)

// MaxKittyCommandBytes bounds the encoded size retained for one Kitty APC.
// Kitty commands larger than this are ignored by the observer, but are still
// passed unchanged to the terminal emulator.
const MaxKittyCommandBytes = 8 << 20

const (
	maxKittyHeaderBytes    = 4096
	maxKittyChunkBytes     = 4096
	maxKittyImageChunks    = 8192
	MaxKittyImageBytes     = 16 << 20
	maxKittyImageDimension = 16384
	maxKittyImagePixels    = 16 << 20
	MaxGraphicsEventBytes  = MaxKittyCommandBytes
)

var (
	errUnsafeKittyCommand = errors.New("vt: unsupported or unsafe Kitty graphics command")
	errKittyUpload        = errors.New("vt: invalid or incomplete Kitty image upload")
	errGraphicsEvent      = errors.New("vt: invalid graphics event")
)

// ParseKittyGraphicsCommand parses one complete Kitty APC command.
func ParseKittyGraphicsCommand(raw []byte, cursor Cursor) (KittyGraphicsCommand, error) {
	var command KittyGraphicsCommand
	var found bool
	var parser kittyGraphicsParser
	parser.feed(raw, func([]byte) {}, func(c KittyGraphicsCommand) {
		command, found = c, true
	})
	if !found || len(raw) != len(command.Raw) {
		return KittyGraphicsCommand{}, errGraphicsEvent
	}
	command.Cursor = cursor
	return command, nil
}

// KittyGraphicsCommand is one complete Kitty graphics APC command. Header is
// the comma-separated option text before the first semicolon; Payload is the
// encoded data after it. Raw contains the original ESC_G...ESC\\ sequence.
// Commands are observed only: the VT emulator still receives the original
// bytes, and no reply is synthesized by this parser.
type KittyGraphicsCommand struct {
	Action  string
	Options map[byte]string
	Header  string
	Payload []byte
	Raw     []byte
	Cursor  Cursor // inner VT cursor when the complete APC command was received
}

// ValidateKittyGraphicsRelay checks the subset safe to relay from an agent
// PTY. It rejects local file/shared-memory sources, malformed base64, invalid
// chunk flags, and conflicting image identifiers. It does not validate image
// dimensions or assemble multi-command uploads.
func (c KittyGraphicsCommand) ValidateKittyGraphicsRelay() error {
	if source := c.Options['t']; source != "" && source != "d" {
		return fmt.Errorf("%w: transmission medium %q", errUnsafeKittyCommand, source)
	}
	if len(c.Payload) > maxKittyChunkBytes {
		return fmt.Errorf("%w: encoded chunk exceeds %d bytes", errUnsafeKittyCommand, maxKittyChunkBytes)
	}
	for _, b := range c.Payload {
		if b == '\r' || b == '\n' || b > 0x7f {
			return fmt.Errorf("%w: invalid base64 payload", errUnsafeKittyCommand)
		}
	}
	var decoded [maxKittyChunkBytes]byte
	if _, err := base64.StdEncoding.Strict().Decode(decoded[:], c.Payload); err != nil {
		return fmt.Errorf("%w: invalid base64 payload", errUnsafeKittyCommand)
	}
	if more := c.Options['m']; more != "" && more != "0" && more != "1" {
		return fmt.Errorf("%w: invalid continuation flag", errUnsafeKittyCommand)
	}
	if quiet := c.Options['q']; quiet != "" && quiet != "0" && quiet != "1" && quiet != "2" {
		return fmt.Errorf("%w: invalid response suppression flag", errUnsafeKittyCommand)
	}
	if c.Options['m'] == "1" && len(c.Payload)%4 != 0 {
		return fmt.Errorf("%w: non-final chunk length is not a multiple of four", errUnsafeKittyCommand)
	}
	if c.Options['m'] == "1" && len(c.Payload) == 0 {
		return fmt.Errorf("%w: empty non-final chunk", errUnsafeKittyCommand)
	}
	if c.Options['m'] == "1" && bytes.IndexByte(c.Payload, '=') >= 0 {
		return fmt.Errorf("%w: padding in non-final chunk", errUnsafeKittyCommand)
	}
	if format := c.Options['f']; format != "" && format != "24" && format != "32" && format != "100" {
		return fmt.Errorf("%w: unsupported pixel format %q", errUnsafeKittyCommand, format)
	}
	if _, hasID := c.Options['i']; hasID {
		if _, hasNumber := c.Options['I']; hasNumber {
			return fmt.Errorf("%w: image id and image number are mutually exclusive", errUnsafeKittyCommand)
		}
	}
	for _, key := range []byte{'i', 'I'} {
		if raw := c.Options[key]; raw != "" {
			id, err := strconv.ParseUint(raw, 10, 32)
			if err != nil || (key == 'i' && id == 0) {
				return fmt.Errorf("%w: invalid image identifier", errUnsafeKittyCommand)
			}
		}
	}
	return nil
}

// KittyImageUpload is a complete static image transmission assembled from
// one or more Kitty APC commands. Data is decoded binary image data; Options
// is copied from the first command. Animation, placement, and deletion
// commands are not image uploads and are not accepted by the assembler.
type KittyImageUpload struct {
	Action  string
	Options map[byte]string
	Data    []byte
	Cursor  Cursor // cursor when the final upload chunk was received
}

// KittyGraphicsAssembler joins one Kitty chunked image upload. The zero value
// is ready to use. An invalid command aborts any partial upload so bytes from
// separate images can never be combined.
type KittyGraphicsAssembler struct {
	active  bool
	action  string
	options map[byte]string
	data    []byte
	chunks  int
}

// Push validates and consumes one command. It returns a completed upload only
// after the final chunk has arrived; nil means the image is still incomplete.
func (a *KittyGraphicsAssembler) Push(c KittyGraphicsCommand) (*KittyImageUpload, error) {
	if err := c.ValidateKittyGraphicsRelay(); err != nil {
		a.Reset()
		return nil, err
	}

	more := c.Options['m'] == "1"
	if !a.active {
		if c.Action != "T" && c.Action != "t" {
			a.Reset()
			return nil, fmt.Errorf("%w: expected image transmit action", errKittyUpload)
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(string(c.Payload))
		if err != nil || len(decoded) > MaxKittyImageBytes {
			a.Reset()
			return nil, errKittyUpload
		}
		if more {
			a.active = true
			a.action = c.Action
			a.options = cloneKittyOptions(c.Options)
			delete(a.options, 'm')
			a.data = append(a.data[:0], decoded...)
			a.chunks = 1
			return nil, nil
		}
		if err := validateKittyImage(c.Options, decoded); err != nil {
			a.Reset()
			return nil, err
		}
		options := cloneKittyOptions(c.Options)
		delete(options, 'm')
		return &KittyImageUpload{Action: c.Action, Options: options, Data: decoded, Cursor: c.Cursor}, nil
	}

	if c.Action != "" || !kittyContinuationOptions(c.Options) || a.chunks >= maxKittyImageChunks {
		a.Reset()
		return nil, fmt.Errorf("%w: unexpected command during chunked upload", errKittyUpload)
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(string(c.Payload))
	if err != nil || len(decoded) > MaxKittyImageBytes-len(a.data) {
		a.Reset()
		return nil, fmt.Errorf("%w: image exceeds %d decoded bytes", errKittyUpload, MaxKittyImageBytes)
	}
	a.data = append(a.data, decoded...)
	a.chunks++
	if more {
		return nil, nil
	}
	if err := validateKittyImage(a.options, a.data); err != nil {
		a.Reset()
		return nil, err
	}
	image := &KittyImageUpload{Action: a.action, Options: a.options, Data: a.data, Cursor: c.Cursor}
	a.active, a.action, a.options, a.data, a.chunks = false, "", nil, nil, 0
	return image, nil
}

func validateKittyImage(options map[byte]string, data []byte) error {
	format := options['f']
	if format == "" {
		format = "24"
	}
	if format == "100" {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxKittyImageDimension || cfg.Height > maxKittyImageDimension || int64(cfg.Width)*int64(cfg.Height) > maxKittyImagePixels {
			return fmt.Errorf("%w: invalid or oversized PNG image", errKittyUpload)
		}
		if width, ok := options['s']; ok {
			w, err := strconv.Atoi(width)
			if err != nil || w != cfg.Width {
				return fmt.Errorf("%w: PNG width does not match metadata", errKittyUpload)
			}
		}
		if height, ok := options['v']; ok {
			h, err := strconv.Atoi(height)
			if err != nil || h != cfg.Height {
				return fmt.Errorf("%w: PNG height does not match metadata", errKittyUpload)
			}
		}
		if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
			return fmt.Errorf("%w: invalid PNG content", errKittyUpload)
		}
		return nil
	}
	channels := int64(3)
	if format == "32" {
		channels = 4
	}
	width, errW := strconv.ParseInt(options['s'], 10, 32)
	height, errH := strconv.ParseInt(options['v'], 10, 32)
	if errW != nil || errH != nil || width <= 0 || height <= 0 || width > maxKittyImageDimension || height > maxKittyImageDimension || width*height > maxKittyImagePixels || width*height*channels != int64(len(data)) {
		return fmt.Errorf("%w: invalid raw pixel dimensions or data length", errKittyUpload)
	}
	return nil
}

// DecodeKittyImage decodes a validated static Kitty upload into pixels for
// conversion to another terminal graphics protocol.
func DecodeKittyImage(upload KittyImageUpload) (image.Image, error) {
	format := upload.Options['f']
	if format == "" || format == "100" {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(upload.Data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxSixelPixels {
			return nil, fmt.Errorf("vt: image exceeds Sixel conversion pixel limit")
		}
	} else {
		w, errW := strconv.Atoi(upload.Options['s'])
		h, errH := strconv.Atoi(upload.Options['v'])
		if errW != nil || errH != nil || w <= 0 || h <= 0 || int64(w)*int64(h) > MaxSixelPixels {
			return nil, fmt.Errorf("vt: image exceeds Sixel conversion pixel limit")
		}
	}
	if err := validateKittyImage(upload.Options, upload.Data); err != nil {
		return nil, err
	}
	if format == "" || format == "100" {
		img, _, err := image.Decode(bytes.NewReader(upload.Data))
		return img, err
	}
	w, _ := strconv.Atoi(upload.Options['s'])
	h, _ := strconv.Atoi(upload.Options['v'])
	channels := 3
	if format == "32" {
		channels = 4
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		o := i * channels
		a := byte(255)
		if channels == 4 {
			a = upload.Data[o+3]
		}
		img.Pix[i*4+0] = upload.Data[o]
		img.Pix[i*4+1] = upload.Data[o+1]
		img.Pix[i*4+2] = upload.Data[o+2]
		img.Pix[i*4+3] = a
	}
	return img, nil
}

// Reset aborts an incomplete upload and releases its retained image bytes.
func (a *KittyGraphicsAssembler) Reset() {
	a.active, a.action, a.options, a.data, a.chunks = false, "", nil, nil, 0
}

func kittyContinuationOptions(options map[byte]string) bool {
	if options['m'] != "0" && options['m'] != "1" {
		return false
	}
	for key := range options {
		if key != 'm' && key != 'q' {
			return false
		}
	}
	return true
}

func cloneKittyOptions(options map[byte]string) map[byte]string {
	clone := make(map[byte]string, len(options))
	for key, value := range options {
		clone[key] = value
	}
	return clone
}

type kittyGraphicsParser struct {
	state   kittyScanState
	string  byte // string introducer; BEL terminates OSC only
	dropped bool
	buf     []byte
}

type kittyScanState uint8

const (
	kittyGround kittyScanState = iota
	kittyEscape
	kittyEscapeIntermediate
	kittyCSI
	kittyString
	kittyStringEscape
	kittyAPC
	kittyAPCEscape
)

// feed observes APC sequences across arbitrary Write boundaries. It retains
// at most MaxKittyCommandBytes and only copies a command when it is a Kitty
// graphics command and the callback is configured.
func (p *kittyGraphicsParser) feed(src []byte, write func([]byte), handle func(KittyGraphicsCommand)) {
	start := 0
	for i := 0; i < len(src); {
		if p.state == kittyGround {
			n := bytes.IndexByte(src[i:], 0x1b)
			if n < 0 {
				break
			}
			i += n + 1
			p.state = kittyEscape
			continue
		}
		b := src[i]
		i++
		switch p.state {
		case kittyEscape:
			switch b {
			case '_':
				p.state = kittyAPC
				p.buf = append(p.buf[:0], 0x1b, '_')
			case ']', 'P', '^', 'X':
				p.state, p.string = kittyString, b
			case '[':
				p.state = kittyCSI
			case 0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x2b, 0x2c, 0x2d, 0x2e, 0x2f:
				p.state = kittyEscapeIntermediate
			default:
				p.state = kittyGround
			}
		case kittyEscapeIntermediate:
			if b >= 0x30 && b <= 0x7e {
				p.state = kittyGround
			} else if b == 0x1b {
				p.state = kittyEscape
			}
		case kittyCSI:
			switch {
			case b >= 0x40 && b <= 0x7e:
				p.state = kittyGround
			case b == 0x1b:
				p.state = kittyEscape
			case b == 0x18 || b == 0x1a:
				p.state = kittyGround
			}
		case kittyString:
			if b == 0x1b {
				p.state = kittyStringEscape
			} else if b == 0x18 || b == 0x1a || (p.string == ']' && b == 0x07) {
				p.state = kittyGround
			}
		case kittyStringEscape:
			if b == '\\' || b == 0x18 || b == 0x1a {
				p.state = kittyGround
			} else if b != 0x1b {
				p.state = kittyString
			}
		case kittyAPC:
			switch b {
			case 0x1b:
				p.state = kittyAPCEscape
			case 0x18, 0x1a:
				p.reset()
			default:
				p.append(b)
			}
		case kittyAPCEscape:
			switch b {
			case '\\':
				command, ok := p.finish([]byte{0x1b, '\\'})
				if ok {
					write(src[start:i])
					start = i
					handle(command)
				}
			case 0x18, 0x1a:
				p.reset()
			default:
				p.state = kittyAPC
				if b == 0x1b {
					p.state = kittyAPCEscape
				} else if !p.dropped {
					p.append(0x1b)
					p.append(b)
				}
			}
		}
	}
	if start < len(src) {
		write(src[start:])
	}
}

func (p *kittyGraphicsParser) append(b byte) {
	if p.dropped {
		return
	}
	if len(p.buf) == MaxKittyCommandBytes {
		p.dropped = true
		p.buf = p.buf[:0]
		return
	}
	p.buf = append(p.buf, b)
}

func (p *kittyGraphicsParser) finish(terminator []byte) (KittyGraphicsCommand, bool) {
	var command KittyGraphicsCommand
	valid := false
	if !p.dropped && len(p.buf) >= 3 && p.buf[0] == 0x1b && p.buf[1] == '_' && p.buf[2] == 'G' {
		body := p.buf[3:]
		if i := bytes.IndexByte(body, ';'); i >= 0 && i <= maxKittyHeaderBytes {
			header := string(body[:i])
			if action, options, ok := parseKittyGraphicsHeader(header); ok {
				raw := append([]byte(nil), p.buf...)
				raw = append(raw, terminator...)
				payloadEnd := len(raw) - len(terminator)
				command = KittyGraphicsCommand{
					Action:  action,
					Options: options,
					Header:  header,
					Payload: raw[3+i+1 : payloadEnd],
					Raw:     raw,
				}
				valid = true
			}
		}
	}
	p.reset()
	return command, valid
}

func parseKittyGraphicsHeader(header string) (string, map[byte]string, bool) {
	if header == "" {
		return "", nil, false
	}
	options := make(map[byte]string)
	for header != "" {
		field, rest, more := strings.Cut(header, ",")
		key, value, ok := strings.Cut(field, "=")
		if !ok || len(key) != 1 || value == "" {
			return "", nil, false
		}
		k := key[0]
		if (k < 'a' || k > 'z') && (k < 'A' || k > 'Z') {
			return "", nil, false
		}
		if _, exists := options[k]; exists {
			return "", nil, false
		}
		for i := range value {
			if value[i] < 0x21 || value[i] > 0x7e || value[i] == ',' || value[i] == '=' {
				return "", nil, false
			}
		}
		options[k] = value
		if !more {
			break
		}
		header = rest
	}
	return options['a'], options, true
}

func (p *kittyGraphicsParser) reset() {
	p.state, p.string, p.dropped = kittyGround, 0, false
	if cap(p.buf) > 64<<10 {
		p.buf = nil
		return
	}
	p.buf = p.buf[:0]
}

// GraphicsEvent carries a complete APC and its cursor anchor in the agent VT.
type GraphicsEvent struct {
	Command []byte
	Cursor  Cursor
}

// AppendGraphicsEvent appends one cursor-anchored Kitty APC event to dst.
func AppendGraphicsEvent(dst []byte, event GraphicsEvent) []byte {
	start := len(dst)
	dst = append(dst, make([]byte, 8)...)
	dst = append(dst, event.Command...)
	binary.BigEndian.PutUint32(dst[start:], uint32(len(event.Command)))
	binary.BigEndian.PutUint16(dst[start+4:], uint16(clamp(event.Cursor.X, 0, 65535)))
	binary.BigEndian.PutUint16(dst[start+6:], uint16(clamp(event.Cursor.Y, 0, 65535)))
	return dst
}

// GraphicsReader reads bounded, length-prefixed Kitty APC commands.
type GraphicsReader struct {
	r *bufio.Reader
}

// NewGraphicsReader returns a reader for a graphics event stream.
func NewGraphicsReader(r io.Reader) *GraphicsReader {
	return &GraphicsReader{r: bufio.NewReaderSize(r, 16<<10)}
}

// Next reads the next complete Kitty APC command. It returns io.EOF at a
// clean end of stream and rejects empty or oversized events.
func (r *GraphicsReader) Next() (GraphicsEvent, error) {
	var header [8]byte
	if _, err := io.ReadFull(r.r, header[:]); err != nil {
		return GraphicsEvent{}, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > MaxGraphicsEventBytes {
		return GraphicsEvent{}, errGraphicsEvent
	}
	command := make([]byte, int(n))
	if _, err := io.ReadFull(r.r, command); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return GraphicsEvent{}, err
	}
	return GraphicsEvent{Command: command, Cursor: Cursor{X: int(binary.BigEndian.Uint16(header[4:])), Y: int(binary.BigEndian.Uint16(header[6:]))}}, nil
}
