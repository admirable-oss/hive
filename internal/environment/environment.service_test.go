package environment_test

import (
	"context"
	"testing"

	"github.com/admirable-oss/hive/internal/environment"
)

func TestService(t *testing.T) {
	tempDir := t.TempDir()
	store := environment.NewFilesystemStore(tempDir)
	svc := environment.NewService(store, tempDir)
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
	if err != environment.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
