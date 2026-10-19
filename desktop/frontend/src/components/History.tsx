import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api";
import { store, useStore } from "../store";

const LINES = 5000;

// History shows a pane's scrollback as text, searchable, read on request
// and kept only while it is open.
export function History() {
  const pane = useStore((s) => s.ui.history);
  const name = useStore((s) => s.workspace.panes.find((p) => p.id === s.ui.history)?.name);
  const [lines, setLines] = useState<string[]>();
  const [query, setQuery] = useState("");
  const [at, setAt] = useState(0);
  const list = useRef<HTMLDivElement>(null);
  const close = () => store.setUI({ history: undefined });

  useEffect(() => {
    if (!pane) return;
    let live = true;
    api
      .history(pane, LINES)
      .then((l) => live && setLines(l ?? []))
      .catch((e) => {
        store.fail(e);
        close();
      });
    return () => {
      live = false;
    };
  }, [pane]);

  const matches = useMemo(() => {
    const q = query.toLowerCase();
    if (!q || !lines) return [];
    const out: number[] = [];
    lines.forEach((l, i) => l.toLowerCase().includes(q) && out.push(i));
    return out;
  }, [lines, query]);

  // Show the newest lines first, then each match as it is chosen.
  useEffect(() => {
    const el = list.current;
    if (!el || !lines) return;
    if (matches.length === 0) {
      el.scrollTop = el.scrollHeight;
      return;
    }
    el.querySelector(`[data-line="${matches[Math.min(at, matches.length - 1)]}"]`)?.scrollIntoView({ block: "center" });
  }, [lines, matches, at]);

  if (!pane) return null;
  const current = matches[Math.min(at, matches.length - 1)];
  return (
    <div className="overlay" onMouseDown={close}>
      <div className="history" role="dialog" aria-label={`Scrollback of ${name || "the pane"}`} onMouseDown={(e) => e.stopPropagation()}>
        <div className="history-bar">
          <input
            autoFocus
            type="search"
            placeholder="Search the scrollback…"
            aria-label="Search the scrollback"
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setAt(Math.max(0, matches.length - 1));
            }}
            onKeyDown={(e) => {
              if (e.key === "Escape") close();
              else if (e.key === "Enter" && matches.length > 0)
                setAt((i) => (e.shiftKey ? (i + 1) % matches.length : (i - 1 + matches.length) % matches.length));
              else return;
              e.preventDefault();
            }}
          />
          <span className="quiet" role="status">
            {lines === undefined ? "Reading…" : query ? `${matches.length ? Math.min(at, matches.length - 1) + 1 : 0} of ${matches.length}` : `${lines.length} lines`}
          </span>
          <button onClick={close}>Close</button>
        </div>
        <div className="history-lines" ref={list}>
          {(lines ?? []).map((l, i) => (
            <div key={i} data-line={i} className={"history-line" + (i === current ? " current" : matches.includes(i) ? " match" : "")}>
              {l || " "}
            </div>
          ))}
        </div>
        <p className="quiet history-hint">Enter: previous match · Shift+Enter: next · Esc: close</p>
      </div>
    </div>
  );
}
