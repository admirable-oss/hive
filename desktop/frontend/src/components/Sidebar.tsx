import { AnimatePresence, motion } from "framer-motion";
import { Bell, FolderGit2, GitBranch, ListChecks, Monitor, MoreHorizontal, Plus, Server, TerminalSquare, Workflow } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { cn } from "@/lib/utils";
import { api, type Agent } from "../api";
import { attention, currentTab, show, store, useStore } from "../store";
import { Bee } from "./Bee";
import { BranchItem } from "./Branches";
import { PlanItem } from "./Plans";
import { StateDot } from "./StateDot";
import { visibleRuns, WorkflowItem } from "./Workflows";

export { StateDot } from "./StateDot";

function SectionTitle({ id, children, action }: { id: string; children: React.ReactNode; action?: React.ReactNode }) {
  return (
    <h2 id={id} className="mt-5 mb-1.5 flex items-center justify-between px-2 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase">
      {children}
      {action}
    </h2>
  );
}

function AgentItem({ a }: { a: Agent }) {
  return (
    <motion.li layout initial={{ opacity: 0, x: -8 }} animate={{ opacity: 1, x: 0 }} exit={{ opacity: 0, x: -8 }} transition={{ duration: 0.18 }}>
      <button
        className="group flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left text-sm transition-colors hover:bg-sidebar-accent"
        onClick={() => {
          if (a.pane_id) {
            show(store, a.pane_id);
            api.focusPane(a.pane_id).catch(store.fail);
          }
        }}
        title={a.reason || a.state}
      >
        <StateDot state={a.state} />
        <span className="min-w-0 flex-1 truncate font-medium">{a.name}</span>
        <span className="truncate text-xs text-muted-foreground">
          {a.machine ? `${a.machine} · ` : ""}
          {a.environment_id.split("/").pop()}
        </span>
      </button>
    </motion.li>
  );
}

const connTone: Record<string, string> = {
  connected: "bg-done",
  connecting: "bg-working animate-pulse",
  offline: "bg-destructive",
  outdated: "bg-destructive",
};

// Sidebar lists what needs you, the plans, every environment with its
// tabs, and the machines.
export function Sidebar({ mac }: { mac: boolean }) {
  const ws = useStore((s) => s.workspace);
  const shown = useStore((s) => currentTab(s)?.id);
  const conn = useStore((s) => s.connection);
  const machines = useStore((s) => s.machines);
  const needs = attention(ws);
  const names = ["", ...new Set(ws.environments.map((e) => e.machine ?? "").filter(Boolean))];
  const groups = names.map((machine) => ({ machine, envs: ws.environments.filter((e) => (e.machine ?? "") === machine) })).filter((g) => g.envs.length > 0);

  return (
    <nav className="flex min-h-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground" aria-label="Environments and agents">
      <div className={cn("drag flex h-13 shrink-0 items-center justify-between px-3", mac && "pl-22")}>
        <span className="flex items-center gap-2 text-sm leading-none font-semibold tracking-tight">
          <Bee className="text-[18px]" />
          Hive
        </span>
        <span
          role="status"
          title={conn.message || conn.state}
          className={cn(
            "no-drag flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] text-muted-foreground",
            (conn.state === "offline" || conn.state === "outdated") && "border-destructive/50 text-destructive",
          )}
        >
          <span className={cn("size-1.5 rounded-full", connTone[conn.state] ?? "bg-idle")} />
          {conn.state === "connected" ? conn.session : conn.state}
        </span>
      </div>

      <ScrollArea className="min-h-0 flex-1 px-2">
        <section aria-label="Needs you">
          <SectionTitle id="needs-you">
            <span className="flex items-center gap-1.5">
              <Bell className="size-3" /> Needs you
            </span>
            {needs.length > 0 && <span className="rounded-full bg-blocked/15 px-1.5 text-[10px] text-blocked">{needs.length}</span>}
          </SectionTitle>
          {needs.length === 0 ? (
            <p className="px-2 text-xs text-muted-foreground">No agent is waiting.</p>
          ) : (
            <ul>
              <AnimatePresence initial={false}>
                {needs.map((a) => (
                  <AgentItem key={a.id} a={a} />
                ))}
              </AnimatePresence>
            </ul>
          )}
        </section>

        {(ws.plans ?? []).length > 0 && (
          <section aria-label="Plans">
            <SectionTitle id="plans">
              <span className="flex items-center gap-1.5">
                <ListChecks className="size-3" /> Plans
              </span>
            </SectionTitle>
            <ul className="space-y-1.5">
              <AnimatePresence initial={false}>
                {(ws.plans ?? []).map((p) => (
                  <PlanItem key={p.id} p={p} />
                ))}
              </AnimatePresence>
            </ul>
          </section>
        )}

        {(ws.workflows ?? []).length > 0 && (
          <section aria-label="Workflows">
            <SectionTitle id="workflows">
              <span className="flex items-center gap-1.5">
                <Workflow className="size-3" /> Workflows
              </span>
            </SectionTitle>
            <ul>
              <AnimatePresence initial={false}>
                {visibleRuns(ws.workflows ?? []).map((r) => (
                  <WorkflowItem key={r.id} r={r} />
                ))}
              </AnimatePresence>
            </ul>
          </section>
        )}

        {(ws.branches ?? []).length > 0 && (
          <section aria-label="Branches">
            <SectionTitle id="branches">
              <span className="flex items-center gap-1.5">
                <GitBranch className="size-3" /> Branches
              </span>
            </SectionTitle>
            <ul>
              <AnimatePresence initial={false}>
                {(ws.branches ?? []).map((b) => (
                  <BranchItem key={b.id} b={b} />
                ))}
              </AnimatePresence>
            </ul>
          </section>
        )}

        <section aria-labelledby="envs">
          <SectionTitle
            id="envs"
            action={
              <Button variant="ghost" size="icon-xs" aria-label="New environment" title="New environment" onClick={() => store.setUI({ dialog: { kind: "new-environment" } })}>
                <Plus />
              </Button>
            }
          >
            Environments
          </SectionTitle>
          {ws.environments.length === 0 && <p className="px-2 text-xs text-muted-foreground">No environments yet.</p>}
          {groups.map((g) => (
            <div key={g.machine}>
              {groups.length > 1 && (
                <h3 className="mt-3 mb-1 flex items-center gap-1.5 px-2 text-xs font-medium text-muted-foreground">
                  {g.machine ? <Server className="size-3" /> : <Monitor className="size-3" />}
                  {g.machine || "This computer"}
                </h3>
              )}
              <ul className="space-y-0.5">
                {g.envs.map((e) => (
                  <li key={e.id}>
                    <div className="flex items-center gap-2 px-2 pt-1.5 pb-0.5 text-sm font-medium" title={e.path}>
                      <FolderGit2 className="size-3.5 text-muted-foreground" />
                      <span className="min-w-0 flex-1 truncate">{e.name}</span>
                      {e.branch && (
                        <span className="truncate font-mono text-[11px] text-muted-foreground">
                          {e.branch}
                          {e.dirty ? "*" : ""}
                        </span>
                      )}
                      <StateDot state={e.state} />
                    </div>
                    <ul>
                      {ws.tabs
                        .filter((t) => t.environment_id === e.id)
                        .map((t) => {
                          const agents = ws.agents.filter((a) => a.tab_id === t.id);
                          const current = t.id === shown;
                          return (
                            <li key={t.id} className="relative">
                              {current && <motion.span layoutId="sidebar-current" className="absolute inset-0 rounded-md bg-sidebar-accent" transition={{ type: "spring", stiffness: 500, damping: 40 }} />}
                              <button
                                className={cn(
                                  "relative flex w-full items-center gap-2 rounded-md py-1 pr-2 pl-7 text-left text-sm transition-colors hover:bg-sidebar-accent/60",
                                  current ? "text-foreground" : "text-muted-foreground",
                                )}
                                aria-current={current ? "page" : undefined}
                                onClick={() => {
                                  store.setUI({ env: e.id, tab: t.id, pane: undefined });
                                  api.focusTab(t.id).catch(store.fail);
                                }}
                              >
                                <TerminalSquare className="size-3.5 shrink-0 opacity-60" />
                                <span className="min-w-0 flex-1 truncate">{t.name || agents[0]?.name || "shell"}</span>
                                {agents.length > 1 && <span className="text-xs text-muted-foreground">{agents.length}</span>}
                                <StateDot state={t.state} />
                              </button>
                            </li>
                          );
                        })}
                    </ul>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </section>

        <section aria-labelledby="machines" className="pb-3">
          <SectionTitle
            id="machines"
            action={
              <Button variant="ghost" size="icon-xs" aria-label="Manage machines" title="Manage machines" onClick={() => store.setUI({ dialog: { kind: "machines" } })}>
                <MoreHorizontal />
              </Button>
            }
          >
            Machines
          </SectionTitle>
          {machines.length === 0 ? (
            <p className="px-2 text-xs text-muted-foreground">
              <button className="text-primary underline-offset-4 hover:underline" onClick={() => store.setUI({ dialog: { kind: "machines" } })}>
                Add a machine
              </button>{" "}
              to see its agents here.
            </p>
          ) : (
            <ul>
              {machines.map((m) => (
                <li key={m.name}>
                  <button
                    className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm hover:bg-sidebar-accent"
                    onClick={() => store.setUI({ dialog: { kind: "machines" } })}
                    title={m.connection?.message || m.host}
                  >
                    <span aria-hidden className={cn("size-2 rounded-full ring-1 ring-idle", connTone[m.connection?.state ?? ""] ?? "bg-transparent")} />
                    <span className="min-w-0 flex-1 truncate">{m.name}</span>
                    {m.state === "changed" ? (
                      <span className="text-xs text-blocked">identity changed</span>
                    ) : (
                      <span className="text-xs text-muted-foreground">
                        {(m.counts?.blocked ?? 0) > 0 ? `${m.counts.blocked} need you` : m.connection?.state === "connected" ? "" : (m.connection?.state ?? "off")}
                      </span>
                    )}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </section>
      </ScrollArea>

      <div className="border-t border-sidebar-border p-2.5">
        <Button className="w-full" onClick={() => store.setUI({ dialog: { kind: "new-agent" } })}>
          <Plus /> New agent
        </Button>
      </div>
    </nav>
  );
}
