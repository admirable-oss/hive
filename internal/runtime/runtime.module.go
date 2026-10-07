package runtime

import (
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/protocol"
)

type Module struct {
	Service     Service
	Protocol    protocol.Protocol
	ViewModel   ViewModelService
	Environment *environment.Module
	Process     *process.Module
}

func NewModule(config Config) *Module {
	service := NewService(config)
	protocolService := protocol.NewService()
	codec := protocol.NewJSONCodec(protocol.DefaultMaxMessageSize)

	// Set up environment module
	baseDir := config.baseDir()
	envModule := environment.NewModule(environment.Config{
		BaseDir: baseDir,
	})

	processModule := process.NewModule(process.Config{
		BaseDir: baseDir,
	}, envModule.Service)

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

	// Register process handlers
	process.RegisterHandlers(protocolService, processModule.Service)

	service.protocol = protocolService
	service.codec = codec

	return &Module{
		Service:     service,
		Protocol:    protocolService,
		ViewModel:   NewViewModel(service),
		Environment: envModule,
		Process:     processModule,
	}
}
