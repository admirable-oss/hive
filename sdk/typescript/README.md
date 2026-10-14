# `@hive-oss/plugin-sdk`

Typed TypeScript SDK for Hive plugin processes and daemon protocol 2. Requires
Node.js 20+ to run plugins, and Bun 1.1+ to install, build, and test this SDK.

See [the TypeScript SDK guide](../../docs/plugins/typescript-sdk.md) for
bootstrap steps, `init()` usage, the daemon wire protocol, extension methods,
and error/lifecycle guidance.

```ts
import { init } from "@hive-oss/plugin-sdk";

const plugin = await init({ id: "example-plugin", version: "1.0.0" });
try {
  const workspace = await plugin.hive.workspace.snapshot();
  console.log(`Hive has ${workspace.tabs.length} tabs`);
} finally {
  plugin.close();
}
```

Develop this package with `bun install`, `bun run build`, `bun run typecheck`,
and `bun test`.
