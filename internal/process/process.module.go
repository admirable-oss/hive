package process

import (
	"path/filepath"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/terminal"
)

type Module struct {
	Service Service
}

func NewModule(cfg Config, envService environment.Service, termService ...terminal.Service) *Module {
	environmentsDir := filepath.Join(cfg.BaseDir, "environments")
	store := NewFilesystemStore(environmentsDir)
	runner := NewExecRunner()
	service := NewService(store, envService, runner, environmentsDir, termService...)
	return &Module{
		Service: service,
	}
}
