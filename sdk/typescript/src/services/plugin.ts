import type { PluginContext, PluginEvent } from "../models/context.js";
import { isPluginContext } from "../models/context.js";
import type { Card, Discovery, ExtensionContribution, Status } from "../views/contribution.js";
import { ttlNanos } from "../views/contribution.js";
import { HiveConnection } from "../protocol/connection.js";
import type { ProtocolTransport } from "../protocol/types.js";
import { createHiveAPI, type HiveAPI } from "./hive.js";
import type { InitOptions } from "./options.js";
import { stdin } from "node:process";

export type EventHandler<T = unknown> = (event: PluginEvent<T>, context: PluginContext) => void | Promise<void>;

export interface HivePlugin {
  readonly context: PluginContext;
  readonly hive: HiveAPI;
  on<T = unknown>(eventType: string, handler: EventHandler<T>): this;
  /** Whether the plugin holds a capability now. */
  has(capability: string): boolean;
  /** Show (or clear) one of the cards the manifest declares. */
  setCard(card: Card): Promise<void>;
  /** Show (or clear) one of the status indicators the manifest declares. */
  setStatus(status: Status): Promise<void>;
  /** Say startup is done (otherwise 2s of uptime counts). */
  ready(): Promise<void>;
  /** Ask the user for an optional capability; the user grants it. */
  requestCapability(capability: string, reason?: string): Promise<void>;
  /** What this hive offers, and what this plugin may call. */
  capabilities(): Promise<Discovery>;
  /** @deprecated declare contributions in hive-plugin.toml [contributes]. */
  extend(contribution: ExtensionContribution): this;
  run(): Promise<void>;
  close(): void;
}

/** Initialize the SDK and connect this invocation to the Hive daemon. */
export async function init(options: InitOptions, connector?: (socketPath: string) => ProtocolTransport): Promise<HivePlugin> {
  const context = options.context ?? await readContext();
  if (context.plugin !== options.id) throw new Error(`plugin context ID ${context.plugin} does not match SDK ID ${options.id}`);
  const connection = await HiveConnection.connect(options, connector);
  const hive = createHiveAPI(connection);
  const handlers = new Map<string, EventHandler<any>[]>();
  const plugin: HivePlugin = {
    context,
    hive,
    on<T>(eventType: string, handler: EventHandler<T>): HivePlugin {
      handlers.set(eventType, [...(handlers.get(eventType) ?? []), handler as EventHandler<any>]);
      return plugin;
    },
    has: (capability: string) => (context.capabilities ?? []).includes(capability),
    async setCard(card: Card) {
      const { ttlSeconds, ...rest } = card;
      await hive.call("plugin.card", { ...rest, ttl: ttlNanos(ttlSeconds) });
    },
    async setStatus(status: Status) {
      const { ttlSeconds, ...rest } = status;
      await hive.call("plugin.status", { ...rest, ttl: ttlNanos(ttlSeconds) });
    },
    async ready() { await hive.call("plugin.ready", {}); },
    async requestCapability(capability: string, reason = "") { await hive.call("plugin.request", { capability, reason }); },
    capabilities: () => hive.call<Discovery>("plugin.capabilities", {}),
    extend(_value: ExtensionContribution): HivePlugin {
      throw new Error("UI contributions are declared in hive-plugin.toml [contributes] since plugin API 1; set cards and indicators with setCard/setStatus");
    },
    async run() {
      if (context.trigger === "event" && context.event) {
        const matching = [...(handlers.get(context.event.type) ?? []), ...(handlers.get("*") ?? [])];
        for (const handler of matching) await handler(context.event, context);
      }
    },
    close: () => connection.close(),
  };
  return plugin;
}

/** Read and validate the single JSON invocation frame from stdin. */
export async function readContext(input: AsyncIterable<Uint8Array | string> = stdin): Promise<PluginContext> {
  let raw = "";
  for await (const chunk of input) raw += typeof chunk === "string" ? chunk : new TextDecoder().decode(chunk);
  if (new TextEncoder().encode(raw).byteLength > (1 << 20)) throw new Error("Hive plugin context exceeds 1 MiB");
  let value: unknown;
  try { value = JSON.parse(raw); }
  catch (error) { throw new Error(`invalid Hive plugin context JSON: ${String(error)}`); }
  if (!isPluginContext(value)) throw new Error("invalid Hive plugin context: plugin, trigger and session are required");
  return value;
}
