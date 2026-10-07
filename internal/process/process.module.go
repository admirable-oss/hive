package process

import (
	"path/filepath"
	"github.com/admirable-oss/hive/internal/environment"
)

type Module struct {
	Service Service
}

func NewModule(cfg Config, envService environment.Service) *Module {
	environmentsDir := filepath.Join(cfg.BaseDir, "environments")
	store := NewFilesystemStore(environmentsDir)
	runner := NewExecRunner()
	service := NewService(store, envService, runner, environmentsDir)
	return &Module{
		Service: service,
	}
}
