package environment

import "path/filepath"

type Module struct {
	Service Service
}

func NewModule(cfg Config) *Module {
	environmentsDir := filepath.Join(cfg.BaseDir, "environments")
	store := NewFilesystemStore(environmentsDir)
	service := NewService(store, environmentsDir)
	return &Module{
		Service: service,
	}
}
