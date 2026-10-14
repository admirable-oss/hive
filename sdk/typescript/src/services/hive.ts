import type { Agent, Pane, Process, Tab, TerminalSnapshot, WorkspaceSnapshot } from "../models/workspace.js";
import type { HiveConnection } from "../protocol/connection.js";

export interface HiveAPI {
  call<T>(method: string, params?: unknown): Promise<T>;
  tabs: {
    list(environmentId?: string): Promise<Tab[]>;
    rename(id: string, name: string): Promise<Tab>;
    focus(id: string): Promise<Tab>;
  };
  panes: {
    list(environmentId?: string, tabId?: string): Promise<Pane[]>;
    popup(request: { tab_id: string; spec?: { name?: string; command?: string[]; cwd?: string; env?: Record<string, string> }; width_pct?: number; height_pct?: number }): Promise<Pane>;
  };
  processes: { list(environmentId?: string): Promise<Process[]> };
  agents: { list(all?: boolean): Promise<{ agents: Agent[]; rollups?: unknown }> };
  workspace: { snapshot(): Promise<WorkspaceSnapshot> };
  terminal: { snapshot(processId: string): Promise<TerminalSnapshot> };
}

/** Bind Hive's stable daemon methods to concise typed service methods. */
export function createHiveAPI(connection: HiveConnection): HiveAPI {
  const call = <T>(method: string, params?: unknown) => connection.call<T>(method, params);
  return {
    call,
    tabs: {
      list: (environment_id = "") => call("tab.list", { environment_id }),
      rename: (id, name) => call("tab.rename", { id, name }),
      focus: id => call("tab.focus", { id }),
    },
    panes: {
      list: (environment_id = "", tab_id = "") => call("pane.list", { environment_id, tab_id }),
      popup: request => call("pane.popup", request),
    },
    processes: { list: (environment_id = "") => call("process.list", { environment_id }) },
    agents: { list: (all = true) => call("agent.list", { all }) },
    workspace: { snapshot: () => call("api.snapshot") },
    terminal: { snapshot: process_id => call("terminal.snapshot", { process_id }) },
  };
}
