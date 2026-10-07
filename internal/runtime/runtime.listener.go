package runtime

import "net"

type ListenerFactory interface {
	Listen(network, address string) (net.Listener, error)
}

type NetListenerFactory struct{}

func NewNetListenerFactory() ListenerFactory {
	return NetListenerFactory{}
}

func (NetListenerFactory) Listen(
	network string,
	address string,
) (net.Listener, error) {
	return net.Listen(network, address)
}
