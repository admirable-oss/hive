package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/jsonfile"
	"github.com/admirable-oss/hive/internal/plugin"
)

// pluginBackup is hive-plugins.json in an export: how each plugin was
// set up, so it can be set up the same way again.
type pluginBackup struct {
	Version     int                 `json:"version"`
	ExportedAt  time.Time           `json:"exported_at"`
	HiveVersion string              `json:"hive_version"`
	Plugins     []pluginBackupEntry `json:"plugins"`
}

type pluginBackupEntry struct {
	ID       string                    `json:"id"`
	Version  string                    `json:"version,omitempty"`
	Source   string                    `json:"source"`
	Ref      string                    `json:"ref,omitempty"`
	Registry bool                      `json:"registry,omitempty"`
	Linked   bool                      `json:"linked,omitempty"`
	Enabled  bool                      `json:"enabled"`
	Granted  []string                  `json:"granted,omitempty"`
	Settings *plugin.ConfigurationFile `json:"settings,omitempty"`
	HasData  bool                      `json:"has_data,omitempty"`
}

const backupIndex = "hive-plugins.json"

// maxBackupFile bounds one file read back from an archive.
const maxBackupFile = 256 << 20

func newPluginExportCmd(a *app) *cobra.Command {
	var (
		output   string
		withData bool
	)
	cmd := &cobra.Command{
		Use:   "export [id...]",
		Short: "Back up plugins' settings and grants (and with --data, their data) to an archive",
		Long: `export writes a .tar.gz with every installed plugin's (or the named ones')
source, enabled state, granted capabilities and settings; --data adds their
data directories. ` + "`hive plugin import`" + ` restores it. Caches are never exported.
Settings are plain files: do not keep secrets in them.`,
		Example: `  hive plugin export -o plugins-backup.tar.gz
  hive plugin export pr-status --data -o pr-status.tar.gz`,
		Args: minArgs(0),
		RunE: withTimeout(10*time.Minute, func(ctx context.Context, _ *cobra.Command, args []string) error {
			if output == "" {
				return &usageError{errors.New("give the archive to write with -o")}
			}
			store := a.plugins()
			list, err := store.List(ctx, buildinfo.Get().Version)
			if err != nil {
				return err
			}
			if len(args) > 0 {
				for _, id := range args {
					if !slices.ContainsFunc(list, func(p plugin.Plugin) bool { return p.ID == id }) {
						return fmt.Errorf("%w %q", plugin.ErrNotFound, id)
					}
				}
				list = slices.DeleteFunc(list, func(p plugin.Plugin) bool { return !slices.Contains(args, p.ID) })
			}
			b := pluginBackup{Version: 1, ExportedAt: time.Now().UTC(), HiveVersion: buildinfo.Get().Version, Plugins: []pluginBackupEntry{}}
			for _, p := range list {
				e := pluginBackupEntry{ID: p.ID, Version: p.Version, Source: p.Source, Ref: p.Ref, Registry: p.Registry, Linked: p.Linked, Enabled: p.Enabled, Granted: p.Granted}
				if p.Manifest != nil {
					e.Version = p.Manifest.Version
					var file plugin.ConfigurationFile
					if err := jsonfile.Read(store.SettingsPath(p.Manifest), &file); err == nil {
						e.Settings = &file
					}
				}
				if withData {
					if entries, err := os.ReadDir(store.DataDir(p.ID)); err == nil && len(entries) > 0 {
						e.HasData = true
					}
				}
				b.Plugins = append(b.Plugins, e)
			}
			n, err := writePluginBackup(output, b, store, withData)
			if err != nil {
				return err
			}
			extra := ""
			if withData {
				extra = fmt.Sprintf(" with %d data file(s)", n)
			}
			return a.done(map[string]any{"archive": output, "plugins": len(b.Plugins), "data_files": n}, "exported %d plugin(s) to %s%s", len(b.Plugins), output, extra)
		}),
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "the archive to write (.tar.gz)")
	cmd.Flags().BoolVar(&withData, "data", false, "include the plugins' data directories")
	return cmd
}

func writePluginBackup(output string, b pluginBackup, store *plugin.Store, withData bool) (int, error) {
	f, err := os.OpenFile(output, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	zw := gzip.NewWriter(f)
	tw := tar.NewWriter(zw)
	index, _ := json.MarshalIndent(b, "", "  ")
	files := 0
	err = func() error {
		if err := tw.WriteHeader(&tar.Header{Name: backupIndex, Mode: 0o600, Size: int64(len(index)), ModTime: b.ExportedAt, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err := tw.Write(index); err != nil {
			return err
		}
		if !withData {
			return nil
		}
		for _, e := range b.Plugins {
			if !e.HasData {
				continue
			}
			// Read through an os.Root: a symlink swapped in while walking
			// cannot lead outside the data directory.
			root, err := os.OpenRoot(store.DataDir(e.ID))
			if err != nil {
				return fmt.Errorf("back up data of %s: %w", e.ID, err)
			}
			err = fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if rel == "." || strings.HasPrefix(d.Name(), ".context-") {
					return nil
				}
				name := path.Join("data", e.ID, filepath.ToSlash(rel))
				info, err := d.Info()
				if err != nil {
					return err
				}
				switch {
				case d.IsDir():
					return tw.WriteHeader(&tar.Header{Name: name + "/", Mode: 0o700, ModTime: info.ModTime(), Typeflag: tar.TypeDir})
				case info.Mode().IsRegular():
					if err := tw.WriteHeader(&tar.Header{Name: name, Mode: int64(info.Mode().Perm()), Size: info.Size(), ModTime: info.ModTime(), Typeflag: tar.TypeReg}); err != nil {
						return err
					}
					src, err := root.Open(rel)
					if err != nil {
						return err
					}
					defer src.Close()
					if _, err := io.CopyN(tw, src, info.Size()); err != nil {
						return err
					}
					files++
				}
				return nil // symlinks and devices are not backed up
			})
			_ = root.Close()
			if err != nil {
				return fmt.Errorf("back up data of %s: %w", e.ID, err)
			}
		}
		return nil
	}()
	if cerr := errors.Join(tw.Close(), zw.Close(), f.Close()); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(output)
	}
	return files, err
}

func newPluginImportCmd(a *app) *cobra.Command {
	var withData, force bool
	cmd := &cobra.Command{
		Use:   "import <archive>",
		Short: "Restore plugins' settings and grants (and with --data, their data) from an export",
		Long: `import restores what ` + "`hive plugin export`" + ` saved for every plugin that is
installed: its settings (migrated on its next start when they are older),
enabled state and grants (only capabilities its manifest still asks for).
It does not install plugins; it lists those missing with how to install them.
--data restores data directories, refusing to overwrite one that has files
unless --force.`,
		Args: exactArgs(1),
		RunE: withTimeout(10*time.Minute, func(ctx context.Context, _ *cobra.Command, args []string) error {
			store := a.plugins()
			b, data, err := readPluginBackup(args[0], withData)
			if err != nil {
				return err
			}
			type result struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}
			var results []result
			for _, e := range b.Plugins {
				p, err := store.Get(ctx, e.ID, buildinfo.Get().Version)
				if err != nil {
					how := e.Source
					if e.Registry {
						how = e.ID
					}
					results = append(results, result{e.ID, "not installed: `hive plugin install " + how + "`"})
					continue
				}
				if p.Manifest != nil {
					declared := p.Manifest.Declared()
					granted := slices.DeleteFunc(slices.Clone(e.Granted), func(c string) bool { return !slices.Contains(declared, c) })
					if !p.Manifest.Legacy() {
						granted = append(granted, p.Granted...)
						if _, err := store.SetGranted(e.ID, granted); err != nil {
							return err
						}
					}
					if e.Settings != nil {
						path := store.SettingsPath(p.Manifest)
						if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
							return err
						}
						if err := jsonfile.Write(path, *e.Settings); err != nil {
							return err
						}
					}
				}
				if _, err := store.SetEnabled(e.ID, e.Enabled); err != nil {
					return err
				}
				status := "settings and grants restored"
				if files := data[e.ID]; withData && len(files) > 0 {
					n, err := restorePluginData(store.DataDir(e.ID), files, force)
					if err != nil {
						return fmt.Errorf("%s: %w", e.ID, err)
					}
					status += fmt.Sprintf(", %d data file(s)", n)
				}
				results = append(results, result{e.ID, status})
			}
			_ = a.reloadPlugins(ctx)
			return a.emit(results, func() error {
				for _, r := range results {
					fmt.Fprintf(a.out, "%s: %s\n", r.ID, r.Status)
				}
				return nil
			})
		}),
	}
	cmd.Flags().BoolVar(&withData, "data", false, "also restore data directories")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite data directories that have files")
	return cmd
}

// backupFile is a data file read from an archive.
type backupFile struct {
	rel  string
	dir  bool
	mode os.FileMode
	body []byte
}

func readPluginBackup(name string, withData bool) (pluginBackup, map[string][]backupFile, error) {
	var b pluginBackup
	f, err := os.Open(name)
	if err != nil {
		return b, nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return b, nil, fmt.Errorf("%s is not a plugin export: %w", name, err)
	}
	tr := tar.NewReader(zr)
	data := map[string][]backupFile{}
	found := false
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return b, nil, err
		}
		clean := path.Clean(h.Name)
		if clean != h.Name && clean+"/" != h.Name || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
			return b, nil, fmt.Errorf("unsafe path %q in the archive", h.Name)
		}
		if clean == backupIndex {
			body, err := io.ReadAll(io.LimitReader(tr, maxBackupFile))
			if err != nil {
				return b, nil, err
			}
			if err := json.Unmarshal(body, &b); err != nil {
				return b, nil, fmt.Errorf("read %s: %w", backupIndex, err)
			}
			found = true
			continue
		}
		parts := strings.SplitN(clean, "/", 3)
		if !withData || len(parts) < 3 || parts[0] != "data" || !plugin.ValidID(parts[1]) {
			continue
		}
		file := backupFile{rel: parts[2], dir: h.Typeflag == tar.TypeDir, mode: os.FileMode(h.Mode).Perm() & 0o700}
		switch h.Typeflag {
		case tar.TypeDir:
		case tar.TypeReg:
			if h.Size > maxBackupFile {
				return b, nil, fmt.Errorf("%s is larger than %d bytes", h.Name, maxBackupFile)
			}
			if file.body, err = io.ReadAll(io.LimitReader(tr, maxBackupFile)); err != nil {
				return b, nil, err
			}
		default:
			continue
		}
		data[parts[1]] = append(data[parts[1]], file)
	}
	if !found || b.Version != 1 {
		return b, nil, fmt.Errorf("%s is not a plugin export (no %s, version 1)", name, backupIndex)
	}
	return b, data, nil
}

func restorePluginData(dir string, files []backupFile, force bool) (int, error) {
	if entries, err := os.ReadDir(dir); err == nil {
		has := slices.ContainsFunc(entries, func(e os.DirEntry) bool { return !strings.HasPrefix(e.Name(), ".context-") })
		if has && !force {
			return 0, fmt.Errorf("its data directory %s has files; rerun with --force to overwrite them", dir)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	n := 0
	for _, f := range files {
		target := filepath.Join(dir, filepath.FromSlash(f.rel))
		if rel, err := filepath.Rel(dir, target); err != nil || strings.HasPrefix(rel, "..") {
			return n, fmt.Errorf("unsafe path %q", f.rel)
		}
		if f.dir {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return n, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return n, err
		}
		if err := os.WriteFile(target, f.body, max(f.mode, 0o600)); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
