package protocol

import "context"

type Protocol interface {
	Handle(context.Context, Request) Response
}
