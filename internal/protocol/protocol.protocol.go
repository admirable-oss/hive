package protocol

import (
	"context"
	"encoding/json"
)

type Protocol interface {
	Handle(context.Context, Request) Response
}

func EncodeResult(value any) (json.RawMessage, error) {
	if value == nil {
		return nil, nil
	}
	return json.Marshal(value)
}

func MustEncodeResult(value any) json.RawMessage {
	raw, err := EncodeResult(value)
	if err != nil {
		panic("protocol: encode result: " + err.Error())
	}
	return raw
}
