package environment

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Service interface {
	Create(ctx context.Context, req CreateRequest) (Environment, error)
	Update(ctx context.Context, req UpdateRequest) (Environment, error)
	Get(ctx context.Context, id string) (Environment, error)
	List(ctx context.Context) ([]Environment, error)
	Delete(ctx context.Context, id string) error
}

// IDs become directory names, so they must never contain path separators or
// start with a dot ("." and ".." would escape the environments directory).
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// envName is a portable environment variable name.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidID reports whether id is safe to use as an environment ID.
func ValidID(id string) bool { return idPattern.MatchString(id) }

type service struct {
	store Store
}

func NewService(store Store) Service {
	return &service{store: store}
}

func (s *service) Create(ctx context.Context, req CreateRequest) (Environment, error) {
	if !ValidID(req.ID) {
		return Environment{}, ErrInvalidID
	}
	if err := validateEnv(req.Env); err != nil {
		return Environment{}, err
	}
	env := Environment{
		Schema:    schema,
		ID:        req.ID,
		Name:      strings.ToUpper(req.ID[:1]) + req.ID[1:],
		Env:       req.Env,
		Worktree:  req.Worktree,
		Status:    StatusReady,
		CreatedAt: time.Now(),
	}
	if req.Root != "" {
		root, err := checkRoot(req.Root)
		if err != nil {
			return Environment{}, err
		}
		env.Path = root
	}
	return s.store.Create(ctx, env)
}

// checkRoot resolves root to an absolute, existing directory.
func checkRoot(root string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("%w: %q", ErrInvalidRoot, root)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: %q", ErrInvalidRoot, root)
	}
	return filepath.Clean(root), nil
}

func validateEnv(env map[string]string) error {
	for k := range env {
		if !envName.MatchString(k) {
			return fmt.Errorf("%w: %q", ErrInvalidEnv, k)
		}
	}
	return nil
}

func (s *service) Update(ctx context.Context, req UpdateRequest) (Environment, error) {
	env, err := s.Get(ctx, req.ID)
	if err != nil {
		return Environment{}, err
	}
	if err := validateEnv(req.Set); err != nil {
		return Environment{}, err
	}
	vars := maps.Clone(env.Env)
	if vars == nil {
		vars = map[string]string{}
	}
	maps.Copy(vars, req.Set)
	for _, k := range req.Unset {
		delete(vars, k)
	}
	if len(vars) == 0 {
		vars = nil
	}
	env.Env = vars
	if err := s.store.Update(ctx, env); err != nil {
		return Environment{}, err
	}
	return env, nil
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
