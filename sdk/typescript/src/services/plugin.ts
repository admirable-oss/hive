import type { PluginContext, PluginEvent } from "../models/context.js";
import { isPluginContext } from "../models/context.js";
import type { ExtensionContribution } from "../views/contribution.js";
import { mergeContributions } from "../views/contribution.js";
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
  let contribution: ExtensionContribution = {};
  const plugin: HivePlugin = {
    context,
    hive,
    on<T>(eventType: string, handler: EventHandler<T>): HivePlugin {
      handlers.set(eventType, [...(handlers.get(eventType) ?? []), handler as EventHandler<any>]);
      return plugin;
    },
    extend(value: ExtensionContribution): HivePlugin { contribution = mergeContributions(contribution, value); return plugin; },
    async run() {
      if (Object.keys(contribution).length && !connection.welcome.capabilities?.includes("plugin.contributions")) {
        throw new Error("Hive daemon does not advertise plugin.contributions; upgrade Hive to enable UI extensions");
      }
      if (Object.keys(contribution).length) await hive.call("plugin.contribute", { plugin: options.id, contribution });
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
