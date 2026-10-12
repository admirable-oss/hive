# Compatibility policy

From 1.0.0, Hive follows [semantic versioning](https://semver.org). Within a
major version (1.x), an upgrade never breaks what you built on Hive: scripts,
agents and tools driving it, configuration files, plugins and programs using
the Go SDK.

## What is stable

| Surface | Promise within 1.x |
|---|---|
| **CLI** | Commands, aliases, flags and exit codes stay. `--json` output keeps every field it has, with the same type and meaning. |
| **Wire API** (`hive api schema`) | Methods stay, and their params and results keep every field, with the same type and meaning. `APILevel` only grows. |
| **Go SDK** (`pkg/hiveapi`) | Exported types, fields, functions and methods keep their names and signatures. |
| **Configuration** | Every key keeps working. A key replaced by another is still read, with a warning. |
| **Plugin manifest** (`hive-plugin.toml`) | Every field keeps its meaning. |
| **Agent manifests** (`[detect]`, `[start]`, `[resume]`, `[[rules]]`) | Every field keeps its meaning. |
| **Events** | Event types and their data's fields stay. |
| **Environment variables** | `HIVE_HOME`, `HIVE_CONFIG`, `HIVE_SESSION`, `HIVE_SOCKET_PATH`, `HIVE_BIN`, `HIVE_PANE_ID`, `HIVE_TAB_ID`, `HIVE_ENV_ID`, `HIVE_PLUGIN_*`, `HIVE_SSH` and `HIVE_LOG` stay. |
| **On disk** | A newer 1.x reads everything an older one wrote (environments, layouts, process records, snapshots, plugins, machines). |

`api/v1/` holds the first four surfaces, frozen at 1.0.0, and tests fail on
any change that breaks them (see [api/v1/README.md](../api/v1/README.md)).

## What may change

Additions are not breaking changes:

- new commands, flags, methods, events, configuration keys, manifest fields;
- new fields in JSON output, results and event data (parse JSON leniently:
  ignore fields you do not know);
- new values of an open set, such as agent kinds, event types, process
  status reasons and the `reason` text of an agent's state.

These are not part of the promise and may change in any release:

- human-readable output: tables, wording, colours, layout of the UI (use
  `--json` or `--template` in scripts);
- default key bindings, themes, timings and limits (the documented
  configuration keys still accept the same values);
- anything named internal or marked experimental, including the
  `internal/` Go packages and the shim protocol between the daemon and its
  shims;
- log files' format.

## Old and new together

A client and a daemon of different 1.x versions work together. A newer
client finds an older daemon by its `APILevel` and replaces it on its next
command when no agent can be lost (agents run under shims); otherwise it
says so. A daemon answers a method it does not know with the
`unknown_method` error code, so a client can tell "too old" from "failed".

## Deprecation

A surface that is to go is deprecated first: it keeps working for the rest
of the major version, and using it prints a warning naming its replacement.
It is removed only in the next major version, whose release notes list
every removal and how to migrate.

## Security fixes

A security fix may break a surface when there is no other way to close the
hole. The release notes say so plainly.

## Supported versions

The latest 1.x minor release gets fixes; security fixes go to the latest
two minor releases. See [SECURITY.md](../SECURITY.md).
