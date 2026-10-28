import { toast } from "sonner";
import { createStore, useStore as useZustand, type StoreApi } from "zustand";
import type { Agent, Connection, IdentityCheck, ImageUpdate, MachineStatus, Pane, PairRequest, ScreenUpdate, Settings, Tab, UpdateStatus, Workspace } from "./api";
import { applyUpdate, type Screen } from "./screen";

export type Dialog =
  | { kind: "new-agent"; env?: string }
  | { kind: "prompt"; agent?: string }
  | { kind: "new-environment" }
  | { kind: "machines" }
  | { kind: "observe" }
  // insights shows a machine's time and usage today, and its budgets.
  | { kind: "insights"; machine?: string }
  // plugins lists a machine's plugins: tier, signer, sandbox, revocations.
  | { kind: "plugins" }
  // review shows a branch's diff, its overlaps and gates, and takes
  // comments for its agent.
  | { kind: "review"; branch: string }
  // identity shows a proven identity before it is trusted: for pairing
  // (pair set) or for a saved machine (machine set).
  | { kind: "identity"; check: IdentityCheck; pair?: PairRequest; machine?: string }
  | { kind: "confirm"; title: string; body: string; action: string; run: () => void };

export interface UI {
  // history is the pane whose scrollback is open.
  history?: string;
  // optionIsMeta makes macOS's Option key Alt in terminals.
  optionIsMeta: boolean;
  env?: string;
  tab?: string;
  pane?: string;
  sidebar: boolean;
  palette: boolean;
  dialog?: Dialog;
  // prefix: the prefix key was pressed; the next key runs a [keys] action.
  prefix?: boolean;
}

export interface State {
  connection: Connection;
  workspace: Workspace;
  settings: Settings;
  machines: MachineStatus[];
  update?: UpdateStatus;
  ui: UI;
}

// Screens and images are kept apart from the rest: a busy pane redraws
// itself only.
interface Screens {
  screens: Record<string, Screen | undefined>;
  images: Record<string, ImageUpdate[] | undefined>;
}

export const emptyWorkspace: Workspace = { environments: [], tabs: [], panes: [], agents: [], state: "", counts: {}, plans: [], branches: [], workflows: [] };

// Per-window preferences live in the webview's storage: nothing that
// matters if lost, and never terminal contents.
export function readPref(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function writePref(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // private mode, or storage refused: the preference lasts this session
  }
}

export function initialState(): State {
  return {
    connection: { state: "connecting", session: "default" },
    workspace: emptyWorkspace,
    settings: { notify_on: ["blocked", "done"], system_notifications: false, theme: "auto", prefix: ["ctrl+b"], keys: [], update_channel: "stable" },
    machines: [],
    ui: { sidebar: true, palette: false, optionIsMeta: readPref("hive.optionIsMeta") === "1" },
  };
}

// Store is what the app shows: two zustand stores, and the actions on them.
export class Store {
  readonly app: StoreApi<State> = createStore<State>()(initialState);
  readonly panes: StoreApi<Screens> = createStore<Screens>()(() => ({ screens: {}, images: {} }));
  private pending?: string;
  // onLost is called when a pane's updates skipped one; it asks again.
  onLost: (pane: string) => void = () => {};

  get state(): State {
    return this.app.getState();
  }

  subscribe = (l: () => void) => this.app.subscribe(l);

  set(change: (s: State) => State) {
    this.app.setState(change(this.app.getState()), true);
  }

  setUI(change: Partial<UI>) {
    this.set((s) => ({ ...s, ui: { ...s.ui, ...change } }));
  }

  setWorkspace(w: Workspace) {
    const live = new Set(w.panes.map((p) => p.id));
    this.panes.setState((p) => ({
      screens: Object.fromEntries(Object.entries(p.screens).filter(([id]) => live.has(id))),
      images: Object.fromEntries(Object.entries(p.images).filter(([id]) => live.has(id))),
    }));
    this.set((s) => ({ ...s, workspace: w }));
    if (this.pending && live.has(this.pending)) {
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
    if (error) toast.error(text);
    else toast(text);
  }

  fail = (err: unknown) => {
    this.toast(err instanceof Error ? err.message : String(err), true);
  };

  // --- screens and images ---

  screen(pane: string): Screen | undefined {
    return this.panes.getState().screens[pane];
  }

  imagesOf(pane: string): ImageUpdate[] | undefined {
    return this.panes.getState().images[pane];
  }

  applyScreen(u: ScreenUpdate) {
    const { screen, lost } = applyUpdate(this.screen(u.pane), u);
    this.panes.setState((p) => ({ screens: { ...p.screens, [u.pane]: screen } }));
    if (lost) this.onLost(u.pane);
  }

  // applyImage keeps at most 32 placements a pane, the newest.
  applyImage(u: ImageUpdate) {
    const now = this.imagesOf(u.pane) ?? [];
    let next: ImageUpdate[];
    switch (u.op) {
      case "place":
        next = [...now.filter((i) => i.key !== u.key), u].slice(-32);
        break;
      case "delete":
        next = now.filter((i) => i.image !== u.image);
        break;
      default:
        next = [];
    }
    if (next.length === 0 && now.length === 0) return;
    this.panes.setState((p) => ({ images: { ...p.images, [u.pane]: next } }));
  }
}

export const store = new Store();

export function useStore<T>(select: (s: State) => T, s: Store = store): T {
  return useZustand(s.app, select);
}

export function useScreen(pane: string, s: Store = store): Screen | undefined {
  return useZustand(s.panes, (p) => p.screens[pane]);
}

export function useImages(pane: string, s: Store = store): ImageUpdate[] | undefined {
  return useZustand(s.panes, (p) => p.images[pane]);
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
  return [...w.agents.filter((a) => a.state === "blocked"), ...w.agents.filter((a) => a.state === "done")];
}

// show makes a pane the shown and focused one.
export function show(s: Store, pane: string) {
  const p = s.state.workspace.panes.find((x) => x.id === pane);
  if (!p) return;
  s.setUI({ env: p.environment_id, tab: p.tab_id, pane: p.id });
}
