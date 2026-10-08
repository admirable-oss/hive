package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

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
				_, warnings, err := config.Load(path)
				if err != nil {
					return err
				}
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
