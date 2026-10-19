import { useMemo, useState } from "react";
import { api } from "../api";
import { show, store, useStore } from "../store";
import { runCommand } from "../commands";
import { StateDot } from "./Sidebar";

export interface Entry {
  id: string;
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
  ["Close pane", "close-pane"],
  ["Next agent needing you", "next-attention"],
  ["Toggle sidebar", "toggle-sidebar"],
  ["Search scrollback…", "history"],
  ["Machines…", "machines"],
  ["Start Hive when I log in (on/off)", "toggle-login"],
  ["Option key types Alt in terminals (on/off)", "toggle-option-meta"],
];

// Palette goes to any agent or pane, or runs an action.
export function Palette() {
  const ws = useStore((s) => s.workspace);
  const [query, setQuery] = useState("");
  const [sel, setSel] = useState(0);
  const close = () => store.setUI({ palette: false });

  const entries = useMemo<Entry[]>(() => {
    const go = (pane: string) => () => {
      show(store, pane);
      api.focusPane(pane).catch(store.fail);
    };
    const out: Entry[] = [];
    for (const a of ws.agents) {
      if (a.pane_id) out.push({ id: "a" + a.id, label: a.name, detail: `${a.kind} · ${a.environment_id}`, state: a.state, run: go(a.pane_id) });
    }
    for (const p of ws.panes) {
      if (!p.agent) out.push({ id: "p" + p.id, label: p.name || "shell", detail: `pane · ${p.environment_id}`, run: go(p.id) });
    }
    for (const [label, cmd] of actions) out.push({ id: "c" + cmd, label, detail: "action", run: () => runCommand(cmd) });
    return out;
  }, [ws]);

  const shown = useMemo(() => {
    return entries
      .map((e) => ({ e, s: match(query, `${e.label} ${e.detail ?? ""}`) }))
      .filter((x) => x.s !== null)
      .sort((a, b) => a.s! - b.s!)
      .slice(0, 50)
      .map((x) => x.e);
  }, [entries, query]);

  const pick = (e?: Entry) => {
    if (!e) return;
    close();
    e.run();
  };

  return (
    <div className="overlay" onMouseDown={close}>
      <div className="palette" role="dialog" aria-label="Command palette" onMouseDown={(e) => e.stopPropagation()}>
        <input
          autoFocus
          role="combobox"
          aria-expanded
          aria-controls="palette-list"
          aria-activedescendant={shown[sel] ? `pal-${shown[sel].id}` : undefined}
          placeholder="Go to an agent or pane, or run an action…"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setSel(0);
          }}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown") setSel((i) => Math.min(i + 1, shown.length - 1));
            else if (e.key === "ArrowUp") setSel((i) => Math.max(i - 1, 0));
            else if (e.key === "Enter") pick(shown[sel]);
            else if (e.key === "Escape") close();
            else return;
            e.preventDefault();
          }}
        />
        <ul id="palette-list" role="listbox" aria-label="Results">
          {shown.map((e, i) => (
            <li
              key={e.id}
              id={`pal-${e.id}`}
              role="option"
              aria-selected={i === sel}
              className={i === sel ? "selected" : undefined}
              onMouseEnter={() => setSel(i)}
              onClick={() => pick(e)}
            >
              <StateDot state={e.state} />
              <span className="item-name">{e.label}</span>
              <span className="item-meta">{e.detail}</span>
            </li>
          ))}
          {shown.length === 0 && <li className="quiet">Nothing matches.</li>}
        </ul>
      </div>
    </div>
  );
}
