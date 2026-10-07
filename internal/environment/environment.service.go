package environment

import (
	"context"
	"path/filepath"
	"strings"
	"time"
)

type Service interface {
	Create(ctx context.Context, id string) (Environment, error)
	Get(ctx context.Context, id string) (Environment, error)
	List(ctx context.Context) ([]Environment, error)
	Delete(ctx context.Context, id string) error
}

type serviceImpl struct {
	store   Store
	baseDir string
}

func NewService(store Store, baseDir string) Service {
	return &serviceImpl{
		store:   store,
		baseDir: baseDir,
	}
}

func (s *serviceImpl) Create(ctx context.Context, id string) (Environment, error) {
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, "\\") {
		return Environment{}, ErrInvalidID
	}
	
	env := Environment{
		ID:        id,
		Name:      strings.ToTitle(id[:1]) + id[1:], // Simple title case for name
		Path:      filepath.Join(s.baseDir, id, "workspace"),
		Status:    StatusReady,
		CreatedAt: time.Now(),
	}

	if err := s.store.Create(ctx, env); err != nil {
		return Environment{}, err
	}

	return env, nil
}

func (s *serviceImpl) Get(ctx context.Context, id string) (Environment, error) {
	return s.store.Get(ctx, id)
}

func (s *serviceImpl) List(ctx context.Context) ([]Environment, error) {
	return s.store.List(ctx)
}

func (s *serviceImpl) Delete(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}
