package runtime

import (
	"context"
	"net"
)

// ListenerFactory opens the daemon's socket. It is a port so tests can swap
// in an in-memory or failing listener.
type ListenerFactory interface {
	Listen(network, address string) (net.Listener, error)
}

type NetListenerFactory struct{}

func NewNetListenerFactory() ListenerFactory { return NetListenerFactory{} }

func (NetListenerFactory) Listen(network, address string) (net.Listener, error) {
	var lc net.ListenConfig
	return lc.Listen(context.Background(), network, address)
}
