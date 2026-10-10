package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/admirable-oss/hive/distribution"
)

// LoadManifests returns the built-in manifests with the user's from
// userDir added: a user manifest with a built-in one's ID replaces it.
// Broken user manifests are skipped with a warning; a broken built-in one
// is a bug and fails.
func LoadManifests(userDir string) ([]*Manifest, []string, error) {
	byID := map[string]*Manifest{}
	entries, err := fs.Glob(distribution.AgentDetection, "agent-detection/*.toml")
	if err != nil {
		return nil, nil, err
	}
	for _, name := range entries {
		data, err := fs.ReadFile(distribution.AgentDetection, name)
		if err != nil {
			return nil, nil, err
		}
		m, err := ParseManifest(data, "built-in "+filepath.Base(name))
		if err != nil {
			return nil, nil, err
		}
		if _, dup := byID[m.ID]; dup {
			return nil, nil, fmt.Errorf("built-in manifests: duplicate id %q", m.ID)
		}
		byID[m.ID] = m
	}

	var warnings []string
	if userDir != "" {
		files, err := filepath.Glob(filepath.Join(userDir, "*.toml"))
		if err != nil {
			return nil, nil, err
		}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					warnings = append(warnings, err.Error())
				}
				continue
			}
			m, err := ParseManifest(data, f)
			if err != nil {
				warnings = append(warnings, err.Error()+" (skipped)")
				continue
			}
			byID[m.ID] = m
		}
	}

	out := make([]*Manifest, 0, len(byID))
	for _, m := range byID {
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b *Manifest) int { return strings.Compare(a.ID, b.ID) })
	return out, warnings, nil
}
