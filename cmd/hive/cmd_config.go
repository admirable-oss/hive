package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/config"
)

func newConfigCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and create the configuration file",
		Long: `Hive reads ~/.config/hive/config.toml ($XDG_CONFIG_HOME/hive/config.toml,
or $HIVE_CONFIG). Every key is optional. Unknown keys and invalid values are
reported and ignored; HIVE_LOG overrides log.level.`,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "path",
			Short: "Print the configuration file's location",
			Args:  noArgs,
			RunE: func(*cobra.Command, []string) error {
				fmt.Fprintln(a.out, a.configPath)
				if _, err := os.Stat(a.configPath); errors.Is(err, fs.ErrNotExist) {
					fmt.Fprintln(a.errOut, "(the file does not exist; defaults apply — create it with `hive config init`)")
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "show",
			Short: "Print the effective configuration",
			Args:  noArgs,
			RunE: func(*cobra.Command, []string) error {
				_, err := a.out.Write(config.Render(a.cfg))
				return err
			},
		},
		&cobra.Command{
			Use:   "default",
			Short: "Print the default configuration",
			Args:  noArgs,
			RunE: func(*cobra.Command, []string) error {
				_, err := a.out.Write(config.Render(config.Defaults()))
				return err
			},
		},
		newConfigInitCmd(a),
		newConfigResetKeysCmd(a),
		&cobra.Command{
			Use:   "validate [file]",
			Short: "Check a configuration file (exit code 1 on any problem)",
			Args:  rangeArgs(0, 1),
			RunE: func(_ *cobra.Command, args []string) error {
				path := a.configPath
				if len(args) == 1 {
					path = args[0]
				}
				if _, err := os.Stat(path); err != nil {
					return err
				}
				cfg, warnings, err := config.Load(path)
				if err != nil {
					return err
				}
				_, uiWarnings := uiOptions(cfg)
				warnings = append(warnings, uiWarnings...)
				for _, w := range warnings {
					fmt.Fprintf(a.out, "%s: %s\n", path, w)
				}
				if len(warnings) > 0 {
					return &codedError{code: exitError, msg: fmt.Sprintf("%d problem(s) in %s", len(warnings), path)}
				}
				fmt.Fprintf(a.out, "%s is valid\n", path)
				return nil
			},
		},
	)
	return cmd
}

func newConfigInitCmd(a *app) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a commented configuration file with the defaults",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			if _, err := os.Stat(a.configPath); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", a.configPath)
			}
			if err := os.MkdirAll(filepath.Dir(a.configPath), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(a.configPath, config.Render(config.Defaults()), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "wrote %s\n", a.configPath)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing file")
	return cmd
}

func newConfigResetKeysCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "reset-keys",
		Short: "Put the default key bindings back in the configuration file",
		Long: `Remove [keys] and every [keys.<mode>] table from the configuration file and
write the default [keys] section in their place. Everything else in the file,
comments included, is kept; the previous file is saved next to it as .bak.`,
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			path := a.configPath
			info, err := os.Stat(path)
			if errors.Is(err, fs.ErrNotExist) {
				fmt.Fprintln(a.out, "there is no configuration file; the default key bindings apply")
				return nil
			}
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			// Only act when the bindings differ, so a second run cannot
			// replace the backup of the user's own bindings.
			out, found := config.ResetKeys(data)
			if cur, _, err := config.Parse(data); !found || (err == nil && reflect.DeepEqual(cur.Keys, config.Defaults().Keys)) {
				fmt.Fprintf(a.out, "%s already uses the default key bindings\n", path)
				return nil
			}
			if _, _, err := config.Parse(out); err != nil {
				return fmt.Errorf("resetting the keys would break %s (%w); edit it by hand", path, err)
			}
			backup := path + ".bak"
			if err := os.WriteFile(backup, data, info.Mode().Perm()); err != nil { //nolint:gosec // the user's own config file
				return fmt.Errorf("back up %s: %w", path, err)
			}
			tmp := path + ".tmp"
			if err := os.WriteFile(tmp, out, info.Mode().Perm()); err != nil { //nolint:gosec // as above
				return err
			}
			if err := os.Rename(tmp, path); err != nil {
				_ = os.Remove(tmp)
				return err
			}
			fmt.Fprintf(a.out, "reset the key bindings in %s (the previous file is %s)\n", path, backup)
			return nil
		},
	}
}
