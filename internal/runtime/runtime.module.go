package runtime

import "github.com/admirable-oss/hive/internal/protocol"

type Module struct {
	Service   Service
	Protocol  protocol.Protocol
	ViewModel ViewModelService
}

func NewModule(config Config) *Module {
	service := NewService(config)
	protocolService := protocol.NewService()
	codec := protocol.NewJSONCodec(protocol.DefaultMaxMessageSize)

	_ = protocolService.Register(
		"runtime.ping",
		NewPingHandler(),
	)

	service.protocol = protocolService
	service.codec = codec

	return &Module{
		Service:   service,
		Protocol:  protocolService,
		ViewModel: NewViewModel(service),
	}
}
