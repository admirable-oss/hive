// The one place the frontend reaches Go: the generated bindings of
// HiveService and the app's events. Tests replace this module.
import { Events, System, Window } from "@wailsio/runtime";
import * as Service from "../bindings/github.com/admirable-oss/hive/desktop/hiveservice";
import * as Observer from "../bindings/github.com/admirable-oss/hive/desktop/observerservice";
import type {
  Connection,
  Discovery,
  ImageUpdate,
  ObserveRequest,
  ObservedScreen,
  ObservedView,
  IdentityCheck,
  MachineStatus,
  Manifest,
  PairRequest,
  ScreenUpdate,
  Settings,
  StartAgentRequest,
  Workspace,
} from "../bindings/github.com/admirable-oss/hive/desktop/internal/core/models";

export type {
  Connection,
  Discovery,
  ImageUpdate,
  ObserveRequest,
  ObservedScreen,
  ObservedView,
  IdentityCheck,
  MachineStatus,
  Manifest,
  PairRequest,
  ScreenUpdate,
  Settings,
  StartAgentRequest,
  Workspace,
};
export type { Nearby, KeyBinding } from "../bindings/github.com/admirable-oss/hive/desktop/internal/core/models";
export type { UpdateStatus } from "../bindings/github.com/admirable-oss/hive/desktop/models";
import type { UpdateStatus } from "../bindings/github.com/admirable-oss/hive/desktop/models";
export type {
  Agent,
  Branch,
  BranchDiff,
  BranchGate,
  BranchOverlap,
  DiffFile,
  Environment,
  Line,
  Plan,
  WorkflowRun,
  PlanTask,
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
  resizePane: (pane: string, direction: "left" | "right" | "up" | "down", cells: number) => Service.ResizePane(pane, direction, cells),
  closePane: (pane: string) => Service.ClosePane(pane),
  renamePane: (pane: string, name: string) => Service.RenamePane(pane, name),
  popOut: (pane: string) => Service.PopOut(pane),
  swapPane: (pane: string, withPane: string) => Service.SwapPane(pane, withPane),
  focusNeighbour: (pane: string, direction: "left" | "right" | "up" | "down") => Service.FocusNeighbour(pane, direction),
  openEnvironmentWindow: (env: string) => Service.OpenEnvironmentWindow(env),
  updateStatus: (): Promise<UpdateStatus> => Service.UpdateStatus(),
  checkForUpdates: (): Promise<UpdateStatus> => Service.CheckForUpdates(),
  installUpdate: () => Service.InstallUpdate(),
  setUpdateChecks: (on: boolean) => Service.SetUpdateChecks(on),
  manifests: (machine: string): Promise<Manifest[]> => Service.Manifests(machine),
  startAgent: (req: StartAgentRequest) => Service.StartAgent(req),
  prompt: (agent: string, text: string) => Service.Prompt(agent, text),
  stopAgent: (agent: string) => Service.StopAgent(agent),
  approveTask: (plan: string, task: string, deny: boolean) => Service.ApproveTask(plan, task, deny),
  cancelPlan: (plan: string) => Service.CancelPlan(plan),
  cancelWorkflowRun: (run: string) => Service.CancelWorkflowRun(run),
  rerunWorkflow: (run: string): Promise<string> => Service.RerunWorkflow(run),
  branchDiff: (branch: string) => Service.BranchDiff(branch),
  commentBranch: (branch: string, path: string, line: number, body: string, send: boolean) => Service.CommentBranch(branch, path, line, body, send),
  sendComments: (branch: string) => Service.SendComments(branch),
  readyBranch: (branch: string): Promise<boolean> => Service.ReadyBranch(branch),
  pushBranch: (branch: string) => Service.PushBranch(branch),
  openPullRequest: (branch: string, draft: boolean, push: boolean): Promise<string> => Service.OpenPullRequest(branch, draft, push),
  cleanBranch: (branch: string, force: boolean) => Service.CleanBranch(branch, force),
  createEnvironment: (machine: string, id: string, dir: string) => Service.CreateEnvironment(machine, id, dir),
  machines: (): Promise<MachineStatus[]> => Service.Machines(),
  refreshMachines: () => Service.RefreshMachines(),
  discover: (): Promise<Discovery> => Service.Discover(),
  checkPair: (req: PairRequest): Promise<IdentityCheck> => Service.CheckPair(req),
  pair: (req: PairRequest, expectID: string) => Service.Pair(req, expectID),
  checkTrust: (name: string): Promise<IdentityCheck> => Service.CheckTrust(name),
  trust: (name: string, expectID: string) => Service.Trust(name, expectID),
  machineAction: (name: string, action: "enable" | "disable" | "revoke" | "remove") => Service.MachineAction(name, action),
  openLink: (url: string) => Service.OpenLink(url),
  history: (pane: string, lines: number): Promise<string[]> => Service.History(pane, lines),
  startAtLogin: (): Promise<boolean> => Service.StartAtLogin(),
  setStartAtLogin: (on: boolean) => Service.SetStartAtLogin(on),
  openObserver: (req: ObserveRequest) => Service.OpenObserver(req),
};

export type Api = typeof api;

export interface Listeners {
  connection: (c: Connection) => void;
  workspace: (w: Workspace) => void;
  screen: (u: ScreenUpdate) => void;
  image: (u: ImageUpdate) => void;
  settings: (s: Settings) => void;
  machines: (m: MachineStatus[]) => void;
  focus: (pane: string) => void;
  command: (name: string) => void;
  drop: (paths: string[]) => void;
  update: (u: UpdateStatus) => void;
}

// listen subscribes to the app's events; the returned function stops.
export function listen(l: Listeners): () => void {
  const offs = [
    Events.On("hive:connection", (e) => l.connection(e.data)),
    Events.On("hive:workspace", (e) => l.workspace(e.data)),
    Events.On("hive:screen", (e) => l.screen(e.data)),
    Events.On("hive:image", (e) => l.image(e.data)),
    Events.On("hive:settings", (e) => l.settings(e.data)),
    Events.On("hive:machines", (e) => l.machines(e.data ?? [])),
    Events.On("hive:focus", (e) => l.focus(e.data.pane)),
    Events.On("hive:command", (e) => l.command(e.data.command)),
    Events.On("hive:drop", (e) => l.drop(e.data.paths ?? [])),
    Events.On("hive:update", (e) => l.update(e.data)),
  ];
  return () => offs.forEach((off) => off());
}

// observerApi is all an observer window calls: it can only look.
export const observerApi = {
  view: (): Promise<ObservedView> => Observer.View(),
  watch: (pane: string) => Observer.Watch(pane),
  unwatch: (pane: string) => Observer.Unwatch(pane),
};

// listenObserved follows this observer window's own events.
export function listenObserved(view: (v: ObservedView) => void, screen: (s: ObservedScreen) => void): () => void {
  let me = "";
  Window.Name()
    .then((n) => (me = n))
    .catch(() => {});
  const mine = (sender?: string) => !me || sender === me;
  const offs = [
    Events.On("hive:observe:view", (e) => mine(e.sender) && view(e.data)),
    Events.On("hive:observe:screen", (e) => mine(e.sender) && screen(e.data)),
  ];
  return () => offs.forEach((off) => off());
}

export function isWindows(): boolean {
  try {
    return System.IsWindows();
  } catch {
    return /Win/.test(navigator.platform);
  }
}

export function isMac(): boolean {
  try {
    return System.IsMac();
  } catch {
    return /Mac/.test(navigator.platform);
  }
}
