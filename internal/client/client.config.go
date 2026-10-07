package client

import "errors"

var ErrInvalidSocketPath = errors.New("client: socket path is required")

type Config struct {
	SocketPath string
}

func (c Config) Validate() error {
	if c.SocketPath == "" {
		return ErrInvalidSocketPath
	}
	return nil
}
