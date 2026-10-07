package runtime

import "net"

// ListenerFactory opens the daemon's socket. It is a port so tests can swap
// in an in-memory or failing listener.
type ListenerFactory interface {
	Listen(network, address string) (net.Listener, error)
}

type NetListenerFactory struct{}

func NewNetListenerFactory() ListenerFactory { return NetListenerFactory{} }

func (NetListenerFactory) Listen(network, address string) (net.Listener, error) {
	return net.Listen(network, address)
}
