package protocol

import (
	"encoding/json"
	"io"
)

type Codec interface {
	Decode(io.Reader, *Request) error
	Encode(io.Writer, Response) error
}

type JSONCodec struct {
	maxMessageSize int64
}

func NewJSONCodec(maxMessageSize int64) Codec {
	return &JSONCodec{
		maxMessageSize: maxMessageSize,
	}
}

func (c *JSONCodec) Decode(reader io.Reader, request *Request) error {
	limited := io.LimitReader(reader, c.maxMessageSize)

	decoder := json.NewDecoder(limited)

	return decoder.Decode(request)
}

func (c *JSONCodec) Encode(writer io.Writer, response Response) error {
	encoder := json.NewEncoder(writer)

	return encoder.Encode(response)
}
