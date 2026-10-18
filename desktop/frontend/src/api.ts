// The one place the frontend reaches Go: the generated bindings of
// HiveService and the app's events. Tests replace this module.
import { Events, System } from "@wailsio/runtime";
import * as Service from "../bindings/github.com/admirable-oss/hive/desktop/hiveservice";
import type {
  Connection,
  Manifest,
  ScreenUpdate,
  Settings,
  StartAgentRequest,
  Workspace,
} from "../bindings/github.com/admirable-oss/hive/desktop/internal/core/models";

export type { Connection, Manifest, ScreenUpdate, Settings, StartAgentRequest, Workspace };
export type {
  Agent,
  Environment,
  Line,
  Pane,
  Rect,
  Run,
  Tab,
  CursorState,
} from "../bindings/github.com/admirable-oss/hive/desktop/internal/core/models";

// viewer names this window to the hub: views it opens are its own.
const viewer =
  typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : String(Math.random()).slice(2);

export const api = {
  connection: () => Service.Connection(),
  workspace: () => Service.Workspace(),
  settings: () => Service.Settings(),
  retry: () => Service.Retry(),
  openView: (pane: string, cols: number, rows: number) => Service.OpenView(viewer, pane, cols, rows),
  closeView: (pane: string) => Service.CloseView(viewer, pane),
  refreshView: (pane: string) => Service.RefreshView(pane),
  input: (pane: string, data: string) => Service.Input(pane, data),
  paste: (pane: string, text: string) => Service.Paste(pane, text),
  pasteClipboard: (pane: string) => Service.PasteClipboard(pane),
  copy: (text: string) => Service.Copy(text),
  resizeTab: (tab: string, cols: number, rows: number) => Service.ResizeTab(tab, cols, rows),
  focusTab: (tab: string) => Service.FocusTab(tab),
  newTab: (env: string) => Service.NewTab(env),
  closeTab: (tab: string) => Service.CloseTab(tab),
  focusPane: (pane: string) => Service.FocusPane(pane),
  splitPane: (pane: string, direction: "left" | "right" | "up" | "down") => Service.SplitPane(pane, direction),
  zoomPane: (pane: string) => Service.ZoomPane(pane),
  closePane: (pane: string) => Service.ClosePane(pane),
  renamePane: (pane: string, name: string) => Service.RenamePane(pane, name),
  popOut: (pane: string) => Service.PopOut(pane),
  manifests: (): Promise<Manifest[]> => Service.Manifests(),
  startAgent: (req: StartAgentRequest) => Service.StartAgent(req),
  prompt: (agent: string, text: string) => Service.Prompt(agent, text),
  stopAgent: (agent: string) => Service.StopAgent(agent),
  createEnvironment: (id: string, dir: string) => Service.CreateEnvironment(id, dir),
  openLink: (url: string) => Service.OpenLink(url),
};

export type Api = typeof api;

export interface Listeners {
  connection: (c: Connection) => void;
  workspace: (w: Workspace) => void;
  screen: (u: ScreenUpdate) => void;
  settings: (s: Settings) => void;
  focus: (pane: string) => void;
  command: (name: string) => void;
}

// listen subscribes to the app's events; the returned function stops.
export function listen(l: Listeners): () => void {
  const offs = [
    Events.On("hive:connection", (e) => l.connection(e.data)),
    Events.On("hive:workspace", (e) => l.workspace(e.data)),
    Events.On("hive:screen", (e) => l.screen(e.data)),
    Events.On("hive:settings", (e) => l.settings(e.data)),
    Events.On("hive:focus", (e) => l.focus(e.data.pane)),
    Events.On("hive:command", (e) => l.command(e.data.command)),
  ];
  return () => offs.forEach((off) => off());
}

export function isMac(): boolean {
  try {
    return System.IsMac();
  } catch {
    return /Mac/.test(navigator.platform);
  }
}
