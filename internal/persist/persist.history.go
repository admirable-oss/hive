package persist

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// History keeps the recent text of panes, for showing it again when a pane
// is restored (session.replay_history). Pane text can hold anything a
// program printed, secrets included, so nothing is kept unless that is
// turned on, and the files are private: <dir>/<pane>.txt, 0600 in a 0700
// directory.
type History struct {
	dir string
}

// NewHistory returns pane history kept in dir.
func NewHistory(dir string) *History { return &History{dir: dir} }

var paneIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (h *History) file(paneID string) (string, error) {
	if !paneIDPattern.MatchString(paneID) {
		return "", fs.ErrInvalid
	}
	return filepath.Join(h.dir, paneID+".txt"), nil
}

// Write replaces a pane's history.
func (h *History) Write(paneID string, text []byte) error {
	path, err := h.file(paneID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(h.dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if _, err := tmp.Write(text); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Read returns a pane's history; fs.ErrNotExist when none is kept.
func (h *History) Read(paneID string) ([]byte, error) {
	path, err := h.file(paneID)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// Prune removes the history of every pane keep rejects (all of it for a
// nil keep) and returns the files it removed, or would remove when dryRun.
func (h *History) Prune(keep func(paneID string) bool, dryRun bool) ([]string, error) {
	entries, err := os.ReadDir(h.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var removed []string
	var errs []error
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".txt")
		if e.IsDir() || !ok || (keep != nil && keep(id)) {
			continue
		}
		path := filepath.Join(h.dir, e.Name())
		if !dryRun {
			if err := os.Remove(path); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		removed = append(removed, path)
	}
	return removed, errors.Join(errs...)
}
