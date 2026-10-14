# TypeScript plugin SDK

`@hive-oss/plugin-sdk` is the official TypeScript client for plugin processes. It
reads Hive's invocation context, connects to the daemon using protocol 2, and
provides typed Hive APIs. It runs on Node.js 20+ and Bun 1.1+; Bun is used to
bootstrap, build, install dependencies, and run the SDK tests.

## Bootstrap a plugin

Create an independently installable plugin package with Bun:

```sh
mkdir my-hive-plugin && cd my-hive-plugin
bun init -y
bun add @hive-oss/plugin-sdk
bun add -d @types/node typescript esbuild
```

The SDK package includes Node type declarations as a development dependency and
ships generated declarations in `dist/`. Plugin projects should set
`moduleResolution` to `NodeNext` or `Bundler`, include `node` in `compilerOptions.types`,
and use `@types/node` when they import Node built-ins. The SDK itself imports
specific `node:` modules and has no ambient `NodeJS.*` references in its public
API, so its declarations resolve consistently in downstream TypeScript builds.

```json
{
  "type": "module",
  "scripts": {
    "build": "tsc -p tsconfig.json && esbuild src/index.ts --bundle --platform=node --format=esm --outfile=dist/index.mjs"
  },
  "dependencies": { "@hive-oss/plugin-sdk": "^0.3.0" },
  "devDependencies": { "@types/node": "^22", "esbuild": "^0.25", "typescript": "^5" }
}
```

## Initialize and use Hive APIs

Hive starts the command declared in `hive-plugin.toml`, writes one JSON
invocation context to stdin, and sets `HIVE_SOCKET_PATH` for the daemon socket.
`init()` validates the context and performs the protocol-2 handshake.

```ts
import { init } from "@hive-oss/plugin-sdk";

const plugin = await init({ id: "my-plugin", version: "1.0.0" });
try {
  if (plugin.context.trigger === "event" && plugin.context.event?.type === "process.output") {
    const snapshot = await plugin.hive.workspace.snapshot();
    const data = plugin.context.event.data as { id?: string } | undefined;
    const pane = snapshot.panes.find(p => p.process_id === data?.id);
    if (pane) await plugin.hive.tabs.rename(pane.tab_id, "Active process");
  }
} finally {
  plugin.close();
}
```

The SDK's typed API currently includes `tabs.list/rename/focus`, `panes.list/popup`,
`processes.list`, `agents.list`, `workspace.snapshot`, `terminal.snapshot`, and
`call<T>(method, params)` for methods not yet wrapped. IDs and request fields use
the daemon JSON names (`tab_id`, `process_id`, `environment_id`). The protocol
schema is available at runtime via `api.schema`; `api.snapshot` returns daemon,
environment, tab, pane, process, agent and rollup state.

## Protocol contract

Frames are UTF-8 JSON objects separated by a newline on a Unix domain socket.
Each connection starts with:

```json
{"version":"2","type":"hello","id":"hello","params":{"client":"hive-plugin:my-plugin","client_version":"1.0.0","capabilities":[]}}
```

The daemon answers with a `welcome` envelope whose `result` contains protocol,
server version, methods and capabilities. Protocol 2 is required. Calls use
string IDs and `request` envelopes; the daemon answers with `response` envelopes
carrying the same ID and either `result` or a structured `{code,message}` error.
The SDK buffers partial frames, preserves trailing frames, and correlates
concurrent requests. Pipe streams use the same connection's `stream`, `data`,
and `close` envelopes; the initial SDK release exposes JSON method calls and
does not yet expose raw pipe helpers. The current daemon API level is 9.

`HiveProtocolError` retains the daemon error code and message. Connection,
timeout, malformed-frame, and handshake failures reject the corresponding
promise. Always call `plugin.close()` in a `finally` block. `HiveConnection` is
exported for protocol-level integrations and tests.

## Events and extension points

Plugins remain ordinary manifest-driven processes. Declare actions and event
subscriptions in `hive-plugin.toml`; `plugin.on(eventType, handler)` dispatches
the current event invocation inside the plugin process. Use `"*"` to handle
every event. `plugin.extend(...)` provides a typed declaration model for
commands, views, and keybindings. It is capability-gated: the plugin host must
advertise `plugin.contributions` and implement `plugin.contribute` before those
declarations can be committed. Until that host capability lands, use manifest
actions and `panes.popup()` for interactive TUI extensions. A popup runs inside
Hive's pane layout and responds to terminal resize/input as a normal process.

The SDK surface is language-neutral by design: Go and TypeScript wrappers map to
the daemon method schema, while transport and JSON types stay documented here.
Do not invent daemon method names; use `api.schema` and `api.snapshot` when
building integrations against a particular Hive version.

## Development

Validate a plugin before linking or submitting it:

```sh
hive plugin validate ./my-hive-plugin
```

This checks the manifest schema, declared commands/events/actions, Hive and
platform compatibility, and required runtimes. It does not publish the plugin
or mark it as reviewed or verified.

From `sdk/typescript/`:

```sh
bun install
bun run build
bun run typecheck
bun test
```

The protocol tests exercise context validation, the hello/welcome exchange,
concurrent request correlation, structured errors, split newline frames, event
dispatch and typed extension methods. A compile-only consumer fixture also
checks that SDK declarations typecheck with `types: []` and no ambient Node
types. `plugins/official/hive-explorer/` is the official
TypeScript example; it opens a pane-based file explorer with preview, search,
and Git status/diff.
