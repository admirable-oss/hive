package environment_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/admirable-oss/hive/internal/environment"
)

func TestService(t *testing.T) {
	tempDir := t.TempDir()
	store := environment.NewFilesystemStore(tempDir)
	svc := environment.NewService(store)
	ctx := context.Background()

	// Create
	env, err := svc.Create(ctx, environment.CreateRequest{ID: "test1"})
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
		if _, err := svc.Create(ctx, environment.CreateRequest{ID: id}); !errors.Is(err, environment.ErrInvalidID) {
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

func TestService_RootedEnvironmentNeverDeletesTheUsersDirectory(t *testing.T) {
	svc := environment.NewService(environment.NewFilesystemStore(t.TempDir()))
	ctx := context.Background()
	project := t.TempDir()
	keep := filepath.Join(project, "main.go")
	if err := os.WriteFile(keep, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, err := svc.Create(ctx, environment.CreateRequest{ID: "proj", Root: project, Env: map[string]string{"API_URL": "http://localhost"}})
	if err != nil {
		t.Fatal(err)
	}
	if env.Path != project || env.Managed || env.Env["API_URL"] != "http://localhost" {
		t.Fatalf("rooted environment = %+v", env)
	}
	if err := svc.Delete(ctx, "proj"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("the user's files must survive deleting the environment: %v", err)
	}
}

func TestService_ManagedWorkspaceIsDeleted(t *testing.T) {
	svc := environment.NewService(environment.NewFilesystemStore(t.TempDir()))
	ctx := context.Background()
	env, err := svc.Create(ctx, environment.CreateRequest{ID: "scratch"})
	if err != nil || !env.Managed {
		t.Fatalf("managed environment = %+v, %v", env, err)
	}
	if err := svc.Delete(ctx, "scratch"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(env.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a managed workspace is deleted with its environment: %v", err)
	}
}

func TestService_RootAndEnvValidation(t *testing.T) {
	svc := environment.NewService(environment.NewFilesystemStore(t.TempDir()))
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "f")
	_ = os.WriteFile(file, nil, 0o644)
	for _, root := range []string{"relative/dir", "/does/not/exist", file} {
		if _, err := svc.Create(ctx, environment.CreateRequest{ID: "x", Root: root}); !errors.Is(err, environment.ErrInvalidRoot) {
			t.Errorf("root %q: got %v, want ErrInvalidRoot", root, err)
		}
	}
	if _, err := svc.Create(ctx, environment.CreateRequest{ID: "x", Env: map[string]string{"1BAD": "v"}}); !errors.Is(err, environment.ErrInvalidEnv) {
		t.Errorf("bad variable name: %v", err)
	}
}

func TestService_UpdateSetsAndUnsetsVariables(t *testing.T) {
	svc := environment.NewService(environment.NewFilesystemStore(t.TempDir()))
	ctx := context.Background()
	if _, err := svc.Create(ctx, environment.CreateRequest{ID: "e", Env: map[string]string{"A": "1", "B": "2"}}); err != nil {
		t.Fatal(err)
	}
	env, err := svc.Update(ctx, environment.UpdateRequest{ID: "e", Set: map[string]string{"C": "3", "A": "9"}, Unset: []string{"B"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Env) != 2 || env.Env["A"] != "9" || env.Env["C"] != "3" {
		t.Fatalf("env after update = %v", env.Env)
	}
	got, _ := svc.Get(ctx, "e")
	if got.Env["A"] != "9" {
		t.Fatal("the update must be stored")
	}
	if _, err := svc.Update(ctx, environment.UpdateRequest{ID: "missing"}); !errors.Is(err, environment.ErrNotFound) {
		t.Fatalf("update of a missing environment: %v", err)
	}
}

func TestStore_MigratesRecordsFromBeforeRootedEnvironments(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "old")
	_ = os.MkdirAll(filepath.Join(dir, "workspace"), 0o755)
	legacy := `{"id":"old","name":"Old","path":"` + filepath.Join(dir, "workspace") + `","status":"ready","created_at":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "environment.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	env, err := environment.NewService(environment.NewFilesystemStore(root)).Get(context.Background(), "old")
	if err != nil {
		t.Fatal(err)
	}
	if !env.Managed || env.Schema != 1 {
		t.Fatalf("legacy environments had managed workspaces: %+v", env)
	}
}
