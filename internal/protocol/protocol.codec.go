package protocol

import (
	"encoding/json"
	"io"
)

type Codec interface {
	EncodeRequest(io.Writer, Request) error
	DecodeRequest(io.Reader, *Request) error
	EncodeResponse(io.Writer, Response) error
	DecodeResponse(io.Reader, *Response) error
}

type JSONCodec struct {
	maxMessageSize int64
}

func NewJSONCodec(maxMessageSize int64) Codec {
	return &JSONCodec{
		maxMessageSize: maxMessageSize,
	}
}

func (c *JSONCodec) EncodeRequest(writer io.Writer, request Request) error {
	encoder := json.NewEncoder(writer)
	return encoder.Encode(request)
}

func (c *JSONCodec) DecodeRequest(reader io.Reader, request *Request) error {
	limited := io.LimitReader(reader, c.maxMessageSize)
	decoder := json.NewDecoder(limited)
	return decoder.Decode(request)
}

func (c *JSONCodec) EncodeResponse(writer io.Writer, response Response) error {
	encoder := json.NewEncoder(writer)
	return encoder.Encode(response)
}

func (c *JSONCodec) DecodeResponse(reader io.Reader, response *Response) error {
	limited := io.LimitReader(reader, c.maxMessageSize)
	decoder := json.NewDecoder(limited)
	return decoder.Decode(response)
}
