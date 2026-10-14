export interface PluginEvent<T = unknown> {
  type: string;
  data?: T;
  timestamp?: string;
}

export interface PluginContext {
  plugin: string;
  trigger: "startup" | "action" | "event" | string;
  action?: string;
  event?: PluginEvent;
  session: string;
  environment?: string;
  tab?: string;
  pane?: string;
}

export function isPluginContext(value: unknown): value is PluginContext {
  if (!value || typeof value !== "object") return false;
  const candidate = value as Partial<PluginContext>;
  return typeof candidate.plugin === "string" && typeof candidate.trigger === "string" && typeof candidate.session === "string";
}
