package runtime

import "context"

type Service interface {
	Start(context.Context) error
	Stop(context.Context) error
}

type Reader interface {
	Runtime(context.Context) (RuntimeModel, error)
}

type Protocol interface {
	Service
	Reader
}
