# Contributing to Hive

Thanks for helping. This page covers how to build, test and lint Hive, and
the conventions the code follows. The architecture overview is in the
[README](README.md#architecture), and design decisions are recorded in
[docs/adr](docs/adr/).

## Setup

Go 1.26+ on macOS or Linux. Tools are pinned and installed into `./bin` on
first use, so there is nothing else to install.

```sh
make build      # ./hive with version information
make check      # what CI runs: vet, lint, race tests (with goleak), go.mod tidiness
make help       # every target
```

To run a daemon you can throw away, point `HIVE_HOME` at a scratch directory.
Keep the path short: macOS limits socket paths to 104 bytes.

```sh
export HIVE_HOME=$(mktemp -d /tmp/hv.XXXX) HIVE_CONFIG=/dev/null HIVE_LOG=debug
./hive demo && ./hive
./hive daemon logs -f        # in another terminal
```

## Pull requests

- Keep changes small and focused. Use [Conventional Commits](https://www.conventionalcommits.org)
  (`feat(process): …`, `fix(tui): …`).
- Every bug fix comes with a test that fails without it.
- `make check` passes. CI also runs the fuzz targets and a snapshot release build.
- A new dependency needs an ADR (`docs/adr/NNNN-title.md`) saying why
  writing the code ourselves is worse.

## Code layout

**One package per domain, one file per role.** The role files a package may
have:

| File | Holds |
|------|-------|
| `pkg.module.go` | Package doc comment, `Config`, `NewModule` (the package's wiring) |
| `pkg.model.go` | Data types |
| `pkg.service.go` | The service contract (interface) and its implementation |
| `pkg.store.go` / `pkg.filesystem_store.go` | Persistence port and its adapter |
| `pkg.protocol.go` | `Register(router, svc)`: wire handlers, and `wireError` mapping domain errors to wire codes |
| `pkg.error.go` | Sentinel errors |
| `pkg.config.go`, `pkg.<topic>.go` | Anything else, named by topic |

Go only reads `_darwin` / `_linux` filename suffixes before the first dot, so
`pkg.thing_linux.go` is **not** OS-specific by name. OS-specific files need an
explicit `//go:build` line.

**Dependencies point down.** `cmd` → `runtime` / `client` / `tui` → domain
packages → leaves. Leaf packages (`protocol`, `jsonfile`, `pgroup`, `platform`,
`logging`, `buildinfo`) import nothing from Hive. Clients (`client`, `tui`)
reach the daemon over the wire only. `depguard` in `.golangci.yml` enforces
this, so a wrong import fails lint.

## Coding standards

### Contracts and wiring

- **The consumer owns the interface.** Declare the small interface you need
  (`Environments{ Get }`) next to the code that uses it; don't import a
  whole service. Keep interfaces to five methods or fewer.
- Constructors accept interfaces and return concrete types. Wiring happens in
  `NewModule` and at the two composition roots (`runtime.NewModule`,
  `cmd/hive`'s `newApp`/`newRootCmd`) and nowhere else.
- No package-level mutable state, no `init()`, no singletons. Loggers, time
  and OS access are injected (see `process.WithOrphanControl`), so tests stay
  deterministic.
- Optional settings use functional options (`process.WithLogger`) or a
  `Config` struct whose zero values mean defaults. Existing call sites must
  keep compiling.

### Concurrency

- Every goroutine has an owner, a way to stop, and a join. If you write
  `go`, also write how it ends. `goleak` runs in every package's `TestMain`
  and fails the build on a leak.
- **No I/O while holding a mutex.** Copy what you need, unlock, then do the
  I/O. (A slow disk used to freeze terminal attach through `publish`.)
- Channels between components are bounded, and overflow has an explicit
  policy. For example, a terminal subscriber that falls behind is cut off and
  told why (`Subscription.Lagged`). It does not silently lose bytes.
- When order matters, give it a single writer. Fanning work out to one
  goroutine per event reorders it (see the TUI's `inputQueue`).

### Errors

- `context.Context` is the first parameter of anything that blocks or does I/O.
- Wrap with `fmt.Errorf("doing X: %w", err)`. Compare with `errors.Is` /
  `errors.AsType`, never `==`. Sentinel errors live in `pkg.error.go`.
- Each package maps its errors to wire codes in one place, its `wireError`.
  Clients branch on `protocol.Error.Code`, never on message text.
- No panics outside `main` and startup wiring (`MustRegister`). A failure
  that a later step cannot handle is logged, not dropped: `_ = store.Update(…)`
  is a bug in daemon code.

### Logging

- `log/slog` only, with a logger passed in. Never `slog.Default()` (lint
  enforces this).
- Key/value pairs in snake_case from a shared vocabulary: `env`, `process`,
  `pid`, `path`, `method`, `status`, `err`.
- Levels: `Debug` is for developers; `Info` is for lifecycle events (started,
  stopped, recovered); `Warn` means something was degraded but handled;
  `Error` means the daemon could not do what it promised.
- Never log terminal content or command output. It can contain secrets.

### Compatibility

- Wire changes are additive: new methods, new optional fields. A breaking
  change bumps `protocol.Version`, and peers on another version get
  `unsupported_version`.
- On-disk JSON files must keep loading after an upgrade. When a format
  changes, add a `schema` field and migrate on read.
- Config keys are never renamed silently. Deprecate with a warning first.

## Tests

- Black-box tests (`package foo_test`) by default. Larger integration suites
  go in a package's `tests/` directory. `export_test.go` is a last resort.
- Table-driven where cases share a shape. Name subtests by behaviour, not
  by input.
- Use fakes for ports (`Store`, `Runner`, `Factory`, `client.Client`) so most
  tests need no processes. When the OS is the point (PTYs, process groups,
  `flock`), test the real thing.
- No sleeps as synchronisation. Poll a condition with a deadline, or wait on
  a channel.
- Fuzz anything that parses untrusted bytes (`FuzzStreamReceive`,
  `FuzzParse`) and register new targets in the `Makefile`'s `FUZZ_TARGETS`.
- The CLI's end-to-end tests re-run the test binary as `hive`
  (`HIVE_CLI_TEST_EXEC=1`). Never let an in-process test autostart a daemon:
  `os.Executable()` is the test binary there.
