# ADR 0003: Dependencies added in M0

- Status: Accepted
- Date: 2026-10-08

Hive keeps its dependency list short. Every module below solves a problem that
would otherwise mean hand-written code that has to be maintained.

| Module | Used for | Why not write it |
|---|---|---|
| `github.com/spf13/cobra` (+ `pflag`) | The CLI: about 30 commands now, about 80 by v1.0, nested help, `--json`, flag parsing | Shell completion for bash, zsh, fish and PowerShell, consistent help and usage errors. The hand-rolled parser had none of these and would not scale. |
| `github.com/pelletier/go-toml/v2` | Reading `config.toml` | TOML is the format users expect (herdr, Cargo, Helix). The parser reports error positions, which our syntax errors pass on. |
| `go.uber.org/goleak` (tests only) | `TestMain` in every package fails on leaked goroutines | Goroutine leaks are the most likely failure in a long-lived daemon, and this catches them in CI. |
| `golang.org/x/sys` | `kinfo_proc` on macOS (process start time for orphan checks) | Already an indirect dependency; the standard library does not expose `sysctl kern.proc.pid`. |

Tool versions are pinned in the `Makefile` and CI, not in `go.mod`:
golangci-lint v2.14.0 and goreleaser v2.18.2. Adding them as `tool`
dependencies would pull several hundred modules into the main module graph.

Rejected:

- viper: we need one TOML file plus one env override.
- lumberjack, for log rotation: about 120 lines in `internal/logging` with
  tests.
- testify: the standard library's `testing` is enough and keeps tests uniform.
