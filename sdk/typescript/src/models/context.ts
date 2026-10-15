export interface PluginEvent<T = unknown> {
  type: string;
  data?: T;
  /** The daemon's event number: events reach a plugin in seq order. */
  seq?: number;
  /** When the event happened (RFC 3339). */
  time?: string;
  /** Events were lost before this one: re-read the state you follow. */
  missed?: boolean;
  /** @deprecated use time. */
  timestamp?: string;
}

export interface PluginContext {
  /** The plugin API version Hive runs the plugin under (absent before API 1). */
  api?: number;
  plugin: string;
  trigger: "startup" | "action" | "event" | "migration" | string;
  action?: string;
  /** This action run's ID (plugin.cancel takes it). */
  run?: string;
  event?: PluginEvent;
  session: string;
  environment?: string;
  tab?: string;
  pane?: string;
  /** The capabilities the plugin holds now. */
  capabilities?: string[];
}

export function isPluginContext(value: unknown): value is PluginContext {
  if (!value || typeof value !== "object") return false;
  const candidate = value as Partial<PluginContext>;
  return typeof candidate.plugin === "string" && typeof candidate.trigger === "string" && typeof candidate.session === "string";
}
