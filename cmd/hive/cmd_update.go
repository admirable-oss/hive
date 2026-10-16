package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/session"
	"github.com/admirable-oss/hive/internal/update"
)

func newUpdateCmd(a *app) *cobra.Command {
	var (
		check, force, noRestart, rollback bool
		tag, from, channel                string
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update hive to the latest release; agents keep running",
		Long: `Download the latest release (or --version) for this system from GitHub, check
it against the release's checksums, make sure it runs, and replace this hive
binary with it. Then every running daemon is restarted on the new binary.
Agents keep running through the restart (their shims are separate processes),
and the new daemon re-attaches to them, so no output is lost.

The binary it replaces is kept beside it (hive.old, hive.exe.old on Windows):
--rollback puts it back and restarts the daemons on it.

A hive installed by Homebrew or Nix is updated by them instead.`,
		Example: `  hive update --check
  hive update
  hive update --version v0.8.0
  hive update --from ./dist/hive_0.8.0_linux_amd64.tar.gz
  hive update --rollback`,
		Args: noArgs,
		RunE: withTimeout(10*time.Minute, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			target, err := os.Executable()
			if err == nil {
				target, err = filepath.EvalSymlinks(target)
			}
			if err != nil {
				return fmt.Errorf("locate the hive binary: %w", err)
			}
			switch {
			case isGoRunBinary(target):
				return fmt.Errorf("this hive was built by `go run`; there is no binary to update")
			case update.Managed(target) == "Homebrew":
				return fmt.Errorf("%s was installed by Homebrew; run `brew upgrade hive`", target)
			case update.Managed(target) != "":
				return fmt.Errorf("%s was installed by %s; update it there", target, update.Managed(target))
			}
			current := buildinfo.Get().Version

			if rollback {
				prev := update.Previous(target)
				version, err := probeVersion(ctx, prev)
				if err != nil {
					return fmt.Errorf("the earlier binary %s does not run: %w", prev, err)
				}
				if err := update.Rollback(target); err != nil {
					return err
				}
				restarted := []string{}
				if !noRestart {
					if restarted, err = a.restartDaemons(ctx, target); err != nil {
						return err
					}
				}
				return a.done(map[string]any{"rolled_back": true, "from": current, "to": version, "path": target, "restarted": restarted}, "rolled hive back %s → %s", current, version)
			}

			var bin []byte
			if from != "" {
				if bin, err = os.ReadFile(from); err != nil {
					return err
				}
				if strings.HasSuffix(from, ".tar.gz") || strings.HasSuffix(from, ".tgz") || strings.HasSuffix(from, ".zip") {
					if bin, err = update.Extract(bin); err != nil {
						return err
					}
				}
			} else {
				src := update.Source{}
				if k := buildinfo.ReleaseKey(); k != "" {
					key, err := update.ParseKey(k)
					if err != nil {
						return err
					}
					src.PublicKey = key
				} else if !check {
					fmt.Fprintln(a.errOut, "hive: this hive was built from source and has no release key: the download's checksum is checked, its signature is not")
				}
				if tag == "" {
					if channel == "" {
						channel = a.cfg.Update.Channel
					}
					if tag, err = src.LatestOn(ctx, channel); err != nil {
						return err
					}
				}
				newer := update.Newer(tag, current)
				if check {
					return a.emit(map[string]any{"current": current, "latest": tag, "update_available": newer}, func() error {
						if !newer {
							_, err := fmt.Fprintf(a.out, "hive %s is up to date (latest release: %s)\n", current, tag)
							return err
						}
						_, err := fmt.Fprintf(a.out, "hive %s is available (this is %s); run `hive update`\n", tag, current)
						return err
					})
				}
				if !newer && !force {
					return a.done(map[string]any{"current": current, "latest": tag, "updated": false}, "hive %s is up to date (latest release: %s)", current, tag)
				}
				if bin, err = src.Fetch(ctx, tag, goruntime.GOOS, goruntime.GOARCH); err != nil {
					return err
				}
			}

			staged, err := update.Stage(target, bin)
			if err != nil {
				return err
			}
			version, err := probeVersion(ctx, staged)
			if err != nil {
				_ = os.Remove(staged) //nolint:gosec // staged is the temporary file update.Stage made
				return fmt.Errorf("the new hive does not run here; keeping %s: %w", current, err)
			}
			if err := update.Commit(staged, target); err != nil {
				return err
			}
			fmt.Fprintf(a.errOut, "hive: installed %s at %s (was %s)\n", version, target, current)

			restarted := []string{}
			if !noRestart {
				if restarted, err = a.restartDaemons(ctx, target); err != nil {
					return err
				}
			}
			msg := fmt.Sprintf("updated hive %s → %s", current, version)
			switch {
			case noRestart:
				msg += "; running daemons keep the old version until `hive daemon restart`"
			case len(restarted) > 0:
				msg += fmt.Sprintf("; restarted the daemon of %s (agents kept running)", strings.Join(restarted, ", "))
			}
			return a.done(map[string]any{"updated": true, "from": current, "to": version, "path": target, "restarted": restarted}, "%s", msg)
		}),
	}
	cmd.Flags().BoolVar(&check, "check", false, "only report whether a newer release exists")
	cmd.Flags().BoolVar(&rollback, "rollback", false, "put back the binary the last update replaced")
	cmd.Flags().StringVar(&tag, "version", "", "install this release (a tag, e.g. v0.8.0) instead of the latest")
	cmd.Flags().StringVar(&channel, "channel", "", "stable, or preview for release candidates too (default update.channel)")
	cmd.Flags().StringVar(&from, "from", "", "install this binary or release archive (.tar.gz) instead of downloading one")
	cmd.Flags().BoolVar(&force, "force", false, "install even when the release is not newer")
	cmd.Flags().BoolVar(&noRestart, "no-restart", false, "leave running daemons on the old binary")
	cmd.MarkFlagsMutuallyExclusive("from", "version")
	cmd.MarkFlagsMutuallyExclusive("from", "check")
	return cmd
}

// probeVersion runs a hive binary and returns the version it reports.
func probeVersion(ctx context.Context, exe string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, exe, "version", "--json")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var out struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil || out.Version == "" {
		return "", fmt.Errorf("it printed no version: %q", stdout.String())
	}
	return out.Version, nil
}

// restartDaemons restarts every session's running daemon on exe, keeping
// its agents, and returns the sessions it restarted.
func (a *app) restartDaemons(ctx context.Context, exe string) ([]string, error) {
	infos, err := session.List(a.base)
	if err != nil {
		return nil, err
	}
	restarted := []string{}
	for _, info := range infos {
		b, err := a.forSession(info.Name)
		if err != nil {
			return restarted, err
		}
		if _, ok := b.daemonRunning(ctx); !ok {
			continue
		}
		if err := b.shutdownDaemon(ctx, false); err != nil {
			return restarted, fmt.Errorf("stop the daemon of session %q: %w", info.Name, err)
		}
		if err := b.spawnDaemon(ctx, exe); err != nil {
			return restarted, fmt.Errorf("start the daemon of session %q: %w", info.Name, err)
		}
		_ = b.client.Close()
		restarted = append(restarted, info.Name)
	}
	return restarted, nil
}
