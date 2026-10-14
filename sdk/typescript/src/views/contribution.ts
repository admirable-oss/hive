/** Declarative contributions an extension can offer to Hive's TUI. */
export interface ExtensionContribution {
  commands?: Array<{ id: string; title: string; description?: string }>;
  views?: Array<{ id: string; title: string; location: "sidebar" | "panel" }>;
  keybindings?: Array<{ key: string; command: string }>;
}

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
