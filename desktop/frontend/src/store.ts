import { useSyncExternalStore } from "react";
import type { Agent, Connection, Pane, ScreenUpdate, Settings, Tab, Workspace } from "./api";
import { applyUpdate, type Screen } from "./screen";

export type Dialog =
  | { kind: "new-agent"; env?: string }
  | { kind: "prompt"; agent?: string }
  | { kind: "new-environment" }
  | { kind: "confirm"; title: string; body: string; action: string; run: () => void };

export interface Toast {
  id: number;
  text: string;
  error?: boolean;
}

export interface UI {
  env?: string;
  tab?: string;
  pane?: string;
  sidebar: boolean;
  palette: boolean;
  dialog?: Dialog;
  toasts: Toast[];
}

export interface State {
  connection: Connection;
  workspace: Workspace;
  settings: Settings;
  ui: UI;
}

export const emptyWorkspace: Workspace = { environments: [], tabs: [], panes: [], agents: [], state: "", counts: {} };

export function initialState(): State {
  return {
    connection: { state: "connecting", session: "default" },
    workspace: emptyWorkspace,
    settings: { notify_on: ["blocked", "done"], system_notifications: false, theme: "auto", prefix: ["ctrl+b"] },
    ui: { sidebar: true, palette: false, toasts: [] },
  };
}

type Listener = () => void;

// Store holds what the app shows. Screens are kept apart from the rest, so
// a busy pane redraws itself only.
export class Store {
  state: State = initialState();
  private screens = new Map<string, Screen>();
  private listeners = new Set<Listener>();
  private screenListeners = new Map<string, Set<Listener>>();
  private toastSeq = 0;
  private pending?: string;
  // lost is called when a pane's updates skipped one; it asks again.
  onLost: (pane: string) => void = () => {};

  subscribe = (l: Listener) => {
    this.listeners.add(l);
    return () => this.listeners.delete(l);
  };

  private emit() {
    this.listeners.forEach((l) => l());
  }

  set(change: (s: State) => State) {
    this.state = change(this.state);
    this.emit();
  }

  setUI(change: Partial<UI>) {
    this.set((s) => ({ ...s, ui: { ...s.ui, ...change } }));
  }

  setWorkspace(w: Workspace) {
    for (const pane of this.screens.keys()) {
      if (!w.panes.some((p) => p.id === pane)) this.screens.delete(pane);
    }
    this.set((s) => ({ ...s, workspace: w }));
    if (this.pending && w.panes.some((p) => p.id === this.pending)) {
      const pane = this.pending;
      this.pending = undefined;
      show(this, pane);
    }
  }

  // reveal shows a pane as soon as the workspace has it (one just made).
  reveal(pane: string) {
    if (this.state.workspace.panes.some((p) => p.id === pane)) show(this, pane);
    else this.pending = pane;
  }

  toast(text: string, error = false) {
    const id = ++this.toastSeq;
    this.setUI({ toasts: [...this.state.ui.toasts, { id, text, error }].slice(-4) });
    setTimeout(() => this.setUI({ toasts: this.state.ui.toasts.filter((t) => t.id !== id) }), error ? 8000 : 4000);
  }

  fail = (err: unknown) => {
    const text = err instanceof Error ? err.message : String(err);
    this.toast(text, true);
  };

  // --- screens ---

  screen(pane: string): Screen | undefined {
    return this.screens.get(pane);
  }

  subscribeScreen(pane: string, l: Listener) {
    let set = this.screenListeners.get(pane);
    if (!set) {
      set = new Set();
      this.screenListeners.set(pane, set);
    }
    set.add(l);
    return () => {
      set.delete(l);
      if (set.size === 0) this.screenListeners.delete(pane);
    };
  }

  applyScreen(u: ScreenUpdate) {
    const { screen, lost } = applyUpdate(this.screens.get(u.pane), u);
    this.screens.set(u.pane, screen);
    this.screenListeners.get(u.pane)?.forEach((l) => l());
    if (lost) this.onLost(u.pane);
  }
}

export const store = new Store();

export function useStore<T>(select: (s: State) => T, s: Store = store): T {
  return useSyncExternalStore(s.subscribe, () => select(s.state));
}

export function useScreen(pane: string, s: Store = store): Screen | undefined {
  return useSyncExternalStore(
    (l) => s.subscribeScreen(pane, l),
    () => s.screen(pane),
  );
}

// --- what is shown ---

// currentTab is the tab shown: the one chosen, else the environment's
// active one, else its first.
export function currentTab(s: State): Tab | undefined {
  const { tabs, environments } = s.workspace;
  const chosen = tabs.find((t) => t.id === s.ui.tab);
  if (chosen) return chosen;
  const env = s.ui.env && environments.some((e) => e.id === s.ui.env) ? s.ui.env : environments[0]?.id;
  const ofEnv = tabs.filter((t) => t.environment_id === env);
  return ofEnv.find((t) => t.active) ?? ofEnv[0];
}

export function currentEnv(s: State): string | undefined {
  return currentTab(s)?.environment_id ?? (s.ui.env || s.workspace.environments[0]?.id);
}

// currentPane is the pane input goes to: the one chosen in this tab, else
// the tab's focused one.
export function currentPane(s: State): Pane | undefined {
  const tab = currentTab(s);
  if (!tab) return undefined;
  const panes = s.workspace.panes.filter((p) => p.tab_id === tab.id);
  return panes.find((p) => p.id === s.ui.pane) ?? panes.find((p) => p.id === tab.focused) ?? panes[0];
}

export function agentOfPane(s: State, pane: string | undefined): Agent | undefined {
  return pane ? s.workspace.agents.find((a) => a.pane_id === pane) : undefined;
}

// attention lists agents that need someone: blocked first, then done.
export function attention(w: Workspace): Agent[] {
  return [
    ...w.agents.filter((a) => a.state === "blocked"),
    ...w.agents.filter((a) => a.state === "done"),
  ];
}

// show makes a pane the shown and focused one.
export function show(s: Store, pane: string) {
  const p = s.state.workspace.panes.find((x) => x.id === pane);
  if (!p) return;
  s.setUI({ env: p.environment_id, tab: p.tab_id, pane: p.id });
}
