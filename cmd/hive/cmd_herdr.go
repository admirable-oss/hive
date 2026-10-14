package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/config"
)

// herdrConfigPath is where herdr keeps its configuration.
func (a *app) herdrConfigPath() string {
	if p := a.getenv("HERDR_CONFIG_PATH"); p != "" {
		return p
	}
	if x := a.getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "herdr", "config.toml")
	}
	return filepath.Join(a.home, ".config", "herdr", "config.toml")
}

func newConfigImportHerdrCmd(a *app) *cobra.Command {
	var (
		from      string
		herdrKeys bool
		write     bool
	)
	cmd := &cobra.Command{
		Use:   "import-herdr",
		Short: "Bring your herdr settings and key bindings into Hive's configuration",
		Long: `Read herdr's config.toml (~/.config/herdr/config.toml, $HERDR_CONFIG_PATH, or
--from) and set the Hive settings that match: key bindings (prefix+ keys go to
prefix mode, the others to terminal mode, navigate keys to navigate mode),
custom commands, theme and custom colours, sidebar, mouse, shell,
notifications, session restore and the tab bar's right side. What has no
Hive equivalent is listed, with why.

With --herdr-keys, herdr's whole default keymap applies first, so the keys
you know from herdr work in Hive. Without --write, the resulting
configuration is printed; with it, it replaces Hive's config file (the old
one is kept beside it). See https://hive.admir-saheta.com/docs/getting-started/migrating-from-herdr/.`,
		Example: `  hive config import-herdr                  # print what it would write
  hive config import-herdr --herdr-keys --write`,
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			if from == "" {
				from = a.herdrConfigPath()
			}
			data, err := os.ReadFile(from)
			if errors.Is(err, fs.ErrNotExist) && herdrKeys {
				data, err = nil, nil // no file of the user's: herdr's keys alone
			}
			if err != nil {
				return err
			}
			cfg, report, err := config.ImportHerdr(a.cfg, data, herdrKeys)
			if err != nil {
				return err
			}
			out := config.Render(cfg)
			if _, warnings, err := config.Parse(out); err != nil {
				return fmt.Errorf("the imported configuration does not read back (%w); please report this", err)
			} else if len(warnings) > 0 {
				return fmt.Errorf("the imported configuration does not read back (%v); please report this", warnings)
			}
			result := map[string]any{"from": from, "mapped": report.Mapped, "skipped": report.Skipped}
			if !write {
				if a.json || a.template != "" {
					result["config"] = string(out)
					return a.emit(result, nil)
				}
				printHerdrReport(a, report)
				_, err := a.out.Write(out)
				return err
			}
			path := a.configPath
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			backup := ""
			if old, err := os.ReadFile(path); err == nil {
				backup = path + ".before-herdr-" + time.Now().Format("20060102-150405")
				if err := os.WriteFile(backup, old, 0o600); err != nil { //nolint:gosec // the user's own config file
					return fmt.Errorf("back up %s: %w", path, err)
				}
			}
			tmp := path + ".tmp"
			if err := os.WriteFile(tmp, out, 0o600); err != nil { //nolint:gosec // as above
				return err
			}
			if err := os.Rename(tmp, path); err != nil {
				_ = os.Remove(tmp)
				return err
			}
			result["path"], result["backup"] = path, backup
			return a.emit(result, func() error {
				printHerdrReport(a, report)
				fmt.Fprintf(a.out, "wrote %s", path)
				if backup != "" {
					fmt.Fprintf(a.out, " (the previous file is %s)", backup)
				}
				_, err := fmt.Fprintln(a.out, "; the UI reads it when it opens")
				return err
			})
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "herdr's config file (default ~/.config/herdr/config.toml)")
	cmd.Flags().BoolVar(&herdrKeys, "herdr-keys", false, "start from herdr's default key bindings")
	cmd.Flags().BoolVar(&write, "write", false, "write Hive's config file instead of printing it")
	return cmd
}

func printHerdrReport(a *app, r config.HerdrImport) {
	fmt.Fprintf(a.errOut, "hive: imported %d herdr setting(s):\n", len(r.Mapped))
	for _, m := range r.Mapped {
		fmt.Fprintf(a.errOut, "  %s\n", m)
	}
	if len(r.Skipped) > 0 {
		fmt.Fprintf(a.errOut, "hive: %d without a Hive equivalent:\n", len(r.Skipped))
		for _, s := range r.Skipped {
			fmt.Fprintf(a.errOut, "  %s\n", s)
		}
	}
}
