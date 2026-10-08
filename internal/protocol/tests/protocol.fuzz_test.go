package protocol_test

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/admirable-oss/hive/internal/protocol"
)

// FuzzStreamReceive feeds arbitrary bytes to the frame reader: it must never
// panic, never return a frame larger than the limit, and always terminate.
func FuzzStreamReceive(f *testing.F) {
	f.Add([]byte(`{"version":"1","type":"request","id":"1","method":"runtime.ping"}` + "\n"))
	f.Add([]byte("{}\n{}\n"))
	f.Add([]byte("not json\n"))
	f.Add([]byte(`{"params":` + string(bytes.Repeat([]byte("["), 300)) + "\n"))
	f.Add([]byte("\n\n\n"))
	f.Add([]byte(`{"id":"unterminated"`))

	const limit = 256
	f.Fuzz(func(t *testing.T, data []byte) {
		server, client := net.Pipe()
		go func() {
			_, _ = client.Write(data)
			_ = client.Close()
		}()
		s := protocol.NewStream(server, limit)
		defer s.Close()
		for range len(data) + 1 {
			var req protocol.Request
			err := s.Receive(&req)
			switch {
			case err == nil, errors.Is(err, protocol.ErrInvalidMessage):
				if len(req.Params) > limit {
					t.Fatalf("decoded %d bytes of params past the %d-byte limit", len(req.Params), limit)
				}
			case errors.Is(err, protocol.ErrMessageTooLarge):
				return // the server closes the connection here
			case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.ErrClosedPipe):
				return
			default:
				t.Fatalf("unexpected error %v", err)
			}
		}
		t.Fatal("Receive did not reach the end of the input")
	})
}
