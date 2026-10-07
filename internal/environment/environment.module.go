// Package environment manages workspaces: the long-lived directories agents
// run in. Its only dependency is the Store port; FilesystemStore is the
// default adapter.
package environment

import "path/filepath"

// Config locates environment storage under the Hive root directory.
type Config struct {
	BaseDir string
}

type Module struct {
	Service Service
}

func NewModule(cfg Config) *Module {
	store := NewFilesystemStore(filepath.Join(cfg.BaseDir, "environments"))
	return &Module{Service: NewService(store)}
}
