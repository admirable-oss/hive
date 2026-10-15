/**
 * Official TypeScript SDK for Hive plugin processes.
 *
 * Import `init()` from this entry point. The implementation stays separated
 * into public models, protocol transport, daemon services, and TUI views.
 */
export { init, readContext } from "./services/plugin.js";
export type { EventHandler, HivePlugin } from "./services/plugin.js";
export type { InitOptions } from "./services/options.js";
export { createHiveAPI } from "./services/hive.js";
export type { HiveAPI } from "./services/hive.js";
export { HiveConnection } from "./protocol/connection.js";
export type { Protocol, ProtocolTransport } from "./protocol/types.js";
export { HiveProtocolError } from "./models/errors.js";
export { isPluginContext } from "./models/context.js";
export type { PluginContext, PluginEvent } from "./models/context.js";
export type { Agent, Pane, Process, Tab, TerminalSnapshot, WorkspaceSnapshot } from "./models/workspace.js";
export { Capability, mergeContributions, ttlNanos } from "./views/contribution.js";
export type { Card, CardRow, Discovery, ExtensionContribution, Link, Status, Tone } from "./views/contribution.js";
