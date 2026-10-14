# Hive plugin SDKs

The official SDKs share the Hive plugin invocation model and protocol 2
contract. Keep their stable method names, JSON field casing, error codes, and
context requirements aligned. Golden JSON frames in `protocol/fixtures/` are
the cross-language wire contract.

| Language | Package | Source |
| --- | --- | --- |
| TypeScript | `@hive-oss/plugin-sdk` | [`typescript/`](typescript/) |
| Rust | `hive-plugin-sdk` | [`rust/`](rust/) |
| Python | `hive-plugin-sdk` / `hive_plugin_sdk` | [`python/`](python/) |
| Go | `github.com/admirable-oss/hive/pkg/pluginsdk` | [`../pkg/pluginsdk/`](../pkg/pluginsdk/) |

Every SDK separates protocol transport, invocation/workspace models, typed Hive
services, and TUI view contribution types. The Hive `api.schema` method remains
the source of truth for all RPC names and parameter/result shapes. `api.snapshot`
is the portable workspace read API. `tab.rename`, `pane.popup`,
`terminal.snapshot`, and `agent.list` are used consistently by the examples.

The daemon protocol is JSON Lines over the provided Unix socket. Clients start
with a protocol 2 `hello` frame and validate the `welcome` before issuing calls.
Frames are limited to 1 MiB. Responses correlate by string ID and errors contain
`code` and `message`. The protocol supports streaming pipes through `stream`,
`data`, and `close`; typed client support for those is being added separately.

Contribution metadata types are exposed consistently. Hive currently supports
interactive TUI extensions by launching a process in `pane.popup`; custom
native sidebar/view contribution RPCs remain gated on a daemon host capability.
SDKs must report this capability clearly and never silently claim a view was
installed.

## Local checks

```sh
cd typescript && bun install && bun run build && bun run typecheck && bun test
cd ../rust && cargo test
cd ../python && python3 -m unittest discover -s tests
cd ../../ && go test ./pkg/pluginsdk ./pkg/hiveapi
```

Package metadata points to `hive-oss/plugin-sdk`. This workspace is currently
checked out as the Hive repository; publishing or moving repositories is a
separate release/ownership operation. Nothing in these local checks pushes or
publishes a package.
