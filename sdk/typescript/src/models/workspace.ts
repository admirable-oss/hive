export interface Tab { id: string; name: string; environment_id?: string; }
export interface Pane { id: string; tab_id: string; process_id?: string; }
export interface Process { id: string; command?: string; args?: string[]; status?: string; }
export interface Agent { id: string; kind?: string; name?: string; pane_id?: string; tab_id?: string; }
export interface WorkspaceSnapshot {
  daemon?: Record<string, unknown>;
  environments?: unknown[];
  tabs: Tab[];
  panes: Pane[];
  processes: Process[];
  agents: Agent[];
  rollups?: unknown;
}
export interface TerminalSnapshot {
  title?: string;
  lines?: string[];
  width?: number;
  height?: number;
}
