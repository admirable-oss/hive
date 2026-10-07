package runtime

type Config struct {
	SocketPath string
	Listener   ListenerFactory
}

func (c Config) Validate() error {
	if c.SocketPath == "" {
		return ErrInvalidSocketPath
	}

	if c.Listener == nil {
		return ErrMissingListener
	}

	return nil
}