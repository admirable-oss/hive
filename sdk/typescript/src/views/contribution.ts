/**
 * Plugin API 1 UI surfaces.
 *
 * What a plugin adds to Hive's UI is declared in hive-plugin.toml under
 * [contributes] (cards, status indicators, contextual actions, palette
 * entries, links); Hive draws them from data and never runs plugin code to
 * draw. At run time a plugin only sets the content of its declared cards and
 * indicators, with HivePlugin.setCard and setStatus.
 */

/** Capabilities a plugin may hold. */
export const Capability = {
  HiveRead: "hive.read",
  HiveWrite: "hive.write",
  HiveInput: "hive.input",
  HiveProcess: "hive.process",
  HiveAdmin: "hive.admin",
  FilesystemWrite: "filesystem.write",
  Network: "network",
  ProcessExec: "process.exec",
} as const;
export type Capability = (typeof Capability)[keyof typeof Capability];

export type Tone = "" | "info" | "success" | "warning" | "error";

/** One line of a card. */
export interface CardRow { label?: string; text: string; tone?: Tone }
/** An http(s) link on a card. */
export interface Link { title: string; url: string }

/** What one of the plugin's declared cards shows; no rows and no links clear it. */
export interface Card {
  card: string;
  /** For environment cards. */
  environment?: string;
  /** For agent cards: the agent's pane. */
  pane?: string;
  rows?: CardRow[];
  links?: Link[];
  /** Seconds it shows (default 1800). */
  ttlSeconds?: number;
}

/** A declared tab-bar indicator's text; no text clears it. */
export interface Status { indicator: string; text?: string; tone?: Tone; ttlSeconds?: number }

/** What the hive a plugin runs in offers it (plugin.capabilities). */
export interface Discovery {
  api: number;
  supported_apis: number[];
  hive_version: string;
  capabilities: Array<{ name: string; description: string; enforced: "daemon" | "sandbox" | "declared" }>;
  events: string[];
  contributions: string[];
  isolation: string[];
  plugin?: string;
  granted?: string[];
  /** Every daemon method this plugin may call. */
  methods?: string[];
}

/** Seconds as the wire's nanosecond duration. */
export function ttlNanos(seconds?: number): number | undefined {
  return seconds === undefined ? undefined : Math.round(seconds * 1e9);
}

/**
 * @deprecated Contributions are declared in hive-plugin.toml [contributes]
 * since plugin API 1; this runtime shape was never served by Hive.
 */
export interface ExtensionContribution {
  commands?: Array<{ id: string; title: string; description?: string }>;
  views?: Array<{ id: string; title: string; location: "sidebar" | "panel" }>;
  keybindings?: Array<{ key: string; command: string }>;
}

/** @deprecated see ExtensionContribution. */
export function mergeContributions(a: ExtensionContribution, b: ExtensionContribution): ExtensionContribution {
  const merged: ExtensionContribution = {
    commands: [...(a.commands ?? []), ...(b.commands ?? [])],
    views: [...(a.views ?? []), ...(b.views ?? [])],
    keybindings: [...(a.keybindings ?? []), ...(b.keybindings ?? [])],
  };
  for (const key of ["commands", "views", "keybindings"] as const) {
    const ids = (merged[key] ?? []).map(item => "id" in item ? item.id : item.key);
    if (new Set(ids).size !== ids.length) throw new Error(`duplicate plugin ${key} contribution ID`);
  }
  return merged;
}
