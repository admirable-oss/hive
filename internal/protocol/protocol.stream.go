package protocol

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
)

// Stream frames newline-delimited JSON messages over one connection.
//
// It keeps a single buffered reader for the connection's whole life. A fresh
// decoder per message would read ahead and silently drop whatever followed the
// frame, which for terminal.attach is the first chunk of terminal data.
type Stream struct {
	conn net.Conn
	r    *bufio.Reader
	max  int
}

func NewStream(conn net.Conn, maxMessageSize int64) *Stream {
	return &Stream{conn: conn, r: bufio.NewReader(conn), max: int(maxMessageSize)}
}

// Send writes v as one JSON frame.
func (s *Stream) Send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.conn.Write(append(data, '\n'))
	return err
}

// Receive reads one frame into v. Undecodable frames return an error wrapping
// ErrInvalidMessage; the stream stays usable because the frame was consumed.
func (s *Stream) Receive(v any) error {
	frame, err := s.readFrame()
	if err != nil {
		return err
	}
	if err := json.Unmarshal(frame, v); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidMessage, err)
	}
	return nil
}

func (s *Stream) readFrame() ([]byte, error) {
	var frame []byte
	for {
		chunk, err := s.r.ReadSlice('\n')
		if len(frame)+len(chunk) > s.max {
			return nil, ErrMessageTooLarge
		}
		frame = append(frame, chunk...)
		switch {
		case err == nil:
			return frame, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(frame) > 0:
			return nil, io.ErrUnexpectedEOF
		default:
			return nil, err
		}
	}
}

// Conn returns the connection for raw use after a hijack. Its reads drain the
// bytes the stream already buffered before reading from the socket.
func (s *Stream) Conn() net.Conn { return bufferedConn{Conn: s.conn, r: s.r} }

// Close closes the underlying connection.
func (s *Stream) Close() error { return s.conn.Close() }

// bufferedConn is a net.Conn whose reads go through a bufio.Reader. Embedding
// keeps every other net.Conn method (Write, Close, deadlines) unchanged.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
