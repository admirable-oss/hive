import type { PluginContext } from "../models/context.js";

export interface InitOptions {
  /** Plugin ID from hive-plugin.toml. */
  id: string;
  version?: string;
  /** Override HIVE_SOCKET_PATH (normally provided by Hive). */
  socketPath?: string;
  /** Invocation context. Defaults to the JSON document sent to stdin by Hive. */
  context?: PluginContext;
  /** Handshake capabilities requested from the daemon. */
  capabilities?: string[];
  timeoutMs?: number;
}
