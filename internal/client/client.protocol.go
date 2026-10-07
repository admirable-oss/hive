package client

import "context"

type Client interface {
	Ping(context.Context) error
	Status(context.Context) (Status, error)
}
