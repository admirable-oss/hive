package task

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/jsonfile"
)

// FilesystemStore keeps one file per plan:
//
//	<root>/<plan-id>.json
//
// A file from a newer hive is left alone and its plan is not loaded; an
// unreadable one is moved aside to <plan-id>.json.corrupt-<time>.
type FilesystemStore struct {
	root string
}

// NewFilesystemStore keeps plans in root.
func NewFilesystemStore(root string) *FilesystemStore { return &FilesystemStore{root: root} }

func (s *FilesystemStore) file(id string) string { return filepath.Join(s.root, id+".json") }

// List reads every plan file.
func (s *FilesystemStore) List(ctx context.Context) ([]Plan, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var plans []Plan
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
			continue
		}
		if ctx.Err() != nil {
			return plans, ctx.Err()
		}
		p, err := s.read(filepath.Join(s.root, name))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		plans = append(plans, p)
	}
	return plans, errors.Join(errs...)
}

func (s *FilesystemStore) read(path string) (Plan, error) {
	var p Plan
	if err := jsonfile.Read(path, &p); err != nil {
		backup := path + ".corrupt-" + time.Now().UTC().Format("20060102T150405Z")
		if rerr := os.Rename(path, backup); rerr != nil {
			return Plan{}, fmt.Errorf("%s: %w (and it could not be moved aside: %w)", path, err, rerr)
		}
		return Plan{}, fmt.Errorf("%s: %w; moved to %s", path, err, backup)
	}
	if err := jsonfile.CheckSchema(path, p.Schema, schema); err != nil {
		return Plan{}, fmt.Errorf("%w: %w", ErrNewer, err)
	}
	if want := strings.TrimSuffix(filepath.Base(path), ".json"); p.ID != want {
		return Plan{}, fmt.Errorf("%s: names plan %q", path, p.ID)
	}
	return p, nil
}

// Put writes a plan atomically.
func (s *FilesystemStore) Put(_ context.Context, p *Plan) error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}
	p.Schema = schema
	return jsonfile.Write(s.file(p.ID), p)
}

// Delete removes a plan's file; a missing one is not an error.
func (s *FilesystemStore) Delete(_ context.Context, id string) error {
	if err := os.Remove(s.file(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
