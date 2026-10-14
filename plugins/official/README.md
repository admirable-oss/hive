# Official Hive plugins

First-party plugins live here and use the same manifest, host, and SDKs as
community plugins. Each directory is an independently versioned package.
Release automation should include runnable output in each package and publish
the immutable repository archive and checksum to the public registry.

- `auto-tab-title/` proposes a tab name from the active agent or terminal
  title, while preserving explicit user names.
- `hive-explorer/` opens a keyboard-driven workspace explorer with file
  preview, filename search, and Git status.

Install a first-party package from a checked out Hive repository with:

```sh
hive plugin install https://github.com/admirable-oss/hive \
  --ref <release-tag> --subdir plugins/official/auto-tab-title
```

Use `plugins/official/hive-explorer` as the explorer subdirectory. Catalog
listings use the same `subdir` field; their digest remains pinned to the full
repository archive.

Bind the explorer action to a key after installation by adding this to
`~/.hive/config.toml`:

```toml
[[keys.command]]
keys = "P"
type = "plugin_action"
plugin = "hive-explorer"
action = "open"
```

Press `Ctrl+B`, then `P` to open its popup in the focused tab. The explorer
uses Node.js 24 or newer; the installer checks the declared runtime.
