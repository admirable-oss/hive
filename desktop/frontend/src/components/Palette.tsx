import { AppWindow, Bot, Command as CommandIcon, FolderOpen, TerminalSquare } from "lucide-react";
import { useMemo, useState } from "react";
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { api } from "../api";
import { runCommand } from "../commands";
import { show, store, useStore } from "../store";
import { StateDot } from "./StateDot";

type Group = "Agents" | "Panes" | "Tabs" | "Environments" | "Actions";
const order: Group[] = ["Agents", "Panes", "Tabs", "Environments", "Actions"];

export interface Entry {
  id: string;
  group: Group;
  label: string;
  detail?: string;
  state?: string;
  run: () => void;
}

// match scores how well query matches text: the letters in order, earlier
// and adjacent ones better; null when they are not all there.
export function match(query: string, text: string): number | null {
  const q = query.toLowerCase().trim();
  if (!q) return 0;
  const t = text.toLowerCase();
  let score = 0,
    from = 0,
    last = -2;
  for (const ch of q) {
    if (ch === " ") continue;
    const i = t.indexOf(ch, from);
    if (i < 0) return null;
    score += i === last + 1 ? 1 : 3 + (i - from);
    last = i;
    from = i + 1;
  }
  return score;
}

const actions: [string, string][] = [
  ["New agent…", "new-agent"],
  ["New tab", "new-tab"],
  ["New environment…", "new-environment"],
  ["Prompt the agent…", "prompt"],
  ["Split right", "split-right"],
  ["Split down", "split-down"],
  ["Zoom pane", "zoom"],
  ["Open pane in a new window", "pop-out"],
  ["Open environment in a new window", "pop-out-env"],
  ["Swap with the next pane", "swap-next"],
  ["Next tab", "next-tab"],
  ["Previous tab", "prev-tab"],
  ["Close tab", "close-tab"],
  ["Close pane", "close-pane"],
  ["Next agent needing you", "next-attention"],
  ["Toggle sidebar", "toggle-sidebar"],
  ["Search scrollback…", "history"],
  ["Machines…", "machines"],
  ["Plugins…", "plugins"],
  ["Open an observer window (read-only)…", "observe"],
  ["Start Hive when I log in (on/off)", "toggle-login"],
  ["Option key types Alt in terminals (on/off)", "toggle-option-meta"],
  ["Check for updates", "check-updates"],
  ["Check for updates automatically (on/off)", "toggle-update-checks"],
];

const icons = { Agents: Bot, Panes: TerminalSquare, Tabs: AppWindow, Environments: FolderOpen, Actions: CommandIcon };

// Palette goes to any agent or pane, or runs an action.
export function Palette() {
  const ws = useStore((s) => s.workspace);
  const update = useStore((s) => s.update?.available);
  const [query, setQuery] = useState("");
  const close = () => store.setUI({ palette: false });

  const entries = useMemo<Entry[]>(() => {
    const go = (pane: string) => () => {
      show(store, pane);
      api.focusPane(pane).catch(store.fail);
    };
    const out: Entry[] = [];
    for (const a of ws.agents) {
      if (a.pane_id) out.push({ id: "a" + a.id, group: "Agents", label: a.name, detail: `${a.kind} · ${a.environment_id}`, state: a.state, run: go(a.pane_id) });
    }
    for (const p of ws.panes) {
      if (!p.agent) out.push({ id: "p" + p.id, group: "Panes", label: p.name || "shell", detail: p.environment_id, run: go(p.id) });
    }
    for (const t of ws.tabs) {
      out.push({
        id: "t" + t.id,
        group: "Tabs",
        label: t.name || "tab",
        detail: t.environment_id,
        run: () => {
          store.setUI({ env: t.environment_id, tab: t.id, pane: undefined });
          api.focusTab(t.id).catch(store.fail);
        },
      });
    }
    for (const e of ws.environments) {
      out.push({ id: "e" + e.id, group: "Environments", label: e.id, detail: e.branch, run: () => store.setUI({ env: e.id, tab: undefined, pane: undefined }) });
    }
    if (update) out.push({ id: "cinstall-update", group: "Actions", label: `Install Hive ${update.version} and restart`, run: () => runCommand("install-update") });
    for (const [label, cmd] of actions) out.push({ id: "c" + cmd, group: "Actions", label, run: () => runCommand(cmd) });
    return out;
  }, [ws, update]);

  const shown = useMemo(
    () =>
      entries
        .map((e) => ({ e, s: match(query, `${e.label} ${e.detail ?? ""}`) }))
        .filter((x) => x.s !== null)
        .sort((a, b) => a.s! - b.s!)
        .slice(0, 50)
        .map((x) => x.e),
    [entries, query],
  );
  const groups = order.map((g) => ({ g, items: shown.filter((e) => e.group === g) })).filter((x) => x.items.length > 0);
  // With a query, results are in score order regardless of group.
  const sections = query.trim() ? [{ g: "Results" as const, items: shown }] : groups;

  const pick = (e: Entry) => {
    close();
    e.run();
  };

  return (
    <Dialog open onOpenChange={(o) => !o && close()}>
      <DialogContent className="top-[18%] translate-y-0 overflow-hidden p-0 sm:max-w-xl" showCloseButton={false}>
        <DialogTitle className="sr-only">Command palette</DialogTitle>
        <DialogDescription className="sr-only">Go to an agent, pane, tab or environment, or run an action</DialogDescription>
        <Command shouldFilter={false} loop className="**:[[cmdk-group-heading]]:px-2 **:[[cmdk-group-heading]]:text-xs **:[[cmdk-group-heading]]:text-muted-foreground">
          <CommandInput autoFocus placeholder="Go to an agent, pane, tab or environment, or run an action…" value={query} onValueChange={setQuery} />
          <CommandList className="max-h-[50vh]">
            <CommandEmpty>Nothing matches.</CommandEmpty>
            {sections.map(({ g, items }) => (
              <CommandGroup key={g} heading={g}>
                {items.map((e) => {
                  const Icon = icons[e.group];
                  return (
                    <CommandItem key={e.id} value={e.id} onSelect={() => pick(e)} className="gap-2.5">
                      {e.state ? <StateDot state={e.state} className="mx-1" /> : <Icon className="text-muted-foreground" />}
                      <span className="truncate">{e.label}</span>
                      {e.detail && <span className="ml-auto truncate text-xs text-muted-foreground">{e.detail}</span>}
                    </CommandItem>
                  );
                })}
              </CommandGroup>
            ))}
          </CommandList>
        </Command>
      </DialogContent>
    </Dialog>
  );
}
