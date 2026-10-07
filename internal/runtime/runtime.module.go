package runtime

import (
	"path/filepath"
	"os"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/protocol"
)

type Module struct {
	Service     Service
	Protocol    protocol.Protocol
	ViewModel   ViewModelService
	Environment *environment.Module
}

func NewModule(config Config) *Module {
	service := NewService(config)
	protocolService := protocol.NewService()
	codec := protocol.NewJSONCodec(protocol.DefaultMaxMessageSize)

	// Set up environment module
	home, _ := os.UserHomeDir()
	envModule := environment.NewModule(environment.Config{
		BaseDir: filepath.Join(home, ".hive"),
	})
	
	// Register runtime handlers
	_ = protocolService.Register(
		"runtime.ping",
		NewPingHandler(),
	)
	_ = protocolService.Register(
		"runtime.status",
		NewStatusHandler(service),
	)
	_ = protocolService.Register(
		"runtime.shutdown",
		NewShutdownHandler(service),
	)

	// Register environment handlers
	environment.RegisterHandlers(protocolService, envModule.Service)

	service.protocol = protocolService
	service.codec = codec

	return &Module{
		Service:     service,
		Protocol:    protocolService,
		ViewModel:   NewViewModel(service),
		Environment: envModule,
	}
}
