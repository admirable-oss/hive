package environment_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/admirable-oss/hive/internal/environment"
)

func TestService(t *testing.T) {
	tempDir := t.TempDir()
	store := environment.NewFilesystemStore(tempDir)
	svc := environment.NewService(store)
	ctx := context.Background()

	// Create
	env, err := svc.Create(ctx, "test1")
	if err != nil {
		t.Fatalf("failed to create environment: %v", err)
	}
	if env.ID != "test1" {
		t.Errorf("expected id test1, got %q", env.ID)
	}

	// Get
	got, err := svc.Get(ctx, "test1")
	if err != nil {
		t.Fatalf("failed to get environment: %v", err)
	}
	if got.ID != "test1" {
		t.Errorf("expected id test1, got %q", got.ID)
	}

	// List
	envs, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("failed to list environments: %v", err)
	}
	if len(envs) != 1 {
		t.Errorf("expected 1 environment, got %d", len(envs))
	}

	// Delete
	err = svc.Delete(ctx, "test1")
	if err != nil {
		t.Fatalf("failed to delete environment: %v", err)
	}

	// Verify Delete
	_, err = svc.Get(ctx, "test1")
	if !errors.Is(err, environment.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestService_RejectsUnsafeIDs(t *testing.T) {
	tempDir := t.TempDir()
	svc := environment.NewService(environment.NewFilesystemStore(tempDir))
	ctx := context.Background()

	for _, id := range []string{"", ".", "..", "../x", "a/b", `a\b`, ".hidden"} {
		if _, err := svc.Create(ctx, id); !errors.Is(err, environment.ErrInvalidID) {
			t.Errorf("Create(%q): expected ErrInvalidID, got %v", id, err)
		}
		if _, err := svc.Get(ctx, id); !errors.Is(err, environment.ErrInvalidID) {
			t.Errorf("Get(%q): expected ErrInvalidID, got %v", id, err)
		}
		if err := svc.Delete(ctx, id); !errors.Is(err, environment.ErrInvalidID) {
			t.Errorf("Delete(%q): expected ErrInvalidID, got %v", id, err)
		}
	}
	if _, err := os.Stat(tempDir); err != nil {
		t.Fatalf("store root must survive unsafe deletes: %v", err)
	}
}
