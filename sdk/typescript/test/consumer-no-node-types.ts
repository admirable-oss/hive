import { init, type HiveAPI, type PluginContext } from "../dist/index.js";

// This is a compile-only fixture. Public SDK declarations must not force plugin
// consumers to add @types/node unless they themselves use Node.js APIs.
declare const context: PluginContext;
declare const api: HiveAPI;
void context.plugin;
void api.tabs.rename("tab-id", "Readable title");
void init({ id: "example-plugin", context });
