package environment

import (
	"context"
	"regexp"
	"strings"
	"time"
)

type Service interface {
	Create(ctx context.Context, id string) (Environment, error)
	Get(ctx context.Context, id string) (Environment, error)
	List(ctx context.Context) ([]Environment, error)
	Delete(ctx context.Context, id string) error
}

// IDs become directory names, so they must never contain path separators or
// start with a dot ("." and ".." would escape the environments directory).
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidID reports whether id is safe to use as an environment ID.
func ValidID(id string) bool { return idPattern.MatchString(id) }

type service struct {
	store Store
}

func NewService(store Store) Service {
	return &service{store: store}
}

func (s *service) Create(ctx context.Context, id string) (Environment, error) {
	if !ValidID(id) {
		return Environment{}, ErrInvalidID
	}
	return s.store.Create(ctx, Environment{
		ID:        id,
		Name:      strings.ToUpper(id[:1]) + id[1:],
		Status:    StatusReady,
		CreatedAt: time.Now(),
	})
}

func (s *service) Get(ctx context.Context, id string) (Environment, error) {
	if !ValidID(id) {
		return Environment{}, ErrInvalidID
	}
	return s.store.Get(ctx, id)
}

func (s *service) List(ctx context.Context) ([]Environment, error) {
	return s.store.List(ctx)
}

func (s *service) Delete(ctx context.Context, id string) error {
	if !ValidID(id) {
		return ErrInvalidID
	}
	return s.store.Delete(ctx, id)
}
