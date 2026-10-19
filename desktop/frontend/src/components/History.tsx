import { Search } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { Kbd } from "@/components/ui/kbd";
import { cn } from "@/lib/utils";
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
  const close = () => {
    store.setUI({ history: undefined });
    setLines(undefined);
    setQuery("");
  };

  useEffect(() => {
    if (!pane) return;
    let live = true;
    api
      .history(pane, LINES)
      .then((l) => live && setLines(l ?? []))
      .catch((e) => {
        store.fail(e);
        store.setUI({ history: undefined });
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

  // The newest lines first, then each match as it is chosen.
  useEffect(() => {
    const el = list.current;
    if (!el || !lines) return;
    if (matches.length === 0) {
      el.scrollTop = el.scrollHeight;
      return;
    }
    el.querySelector(`[data-line="${matches[Math.min(at, matches.length - 1)]}"]`)?.scrollIntoView?.({ block: "center" });
  }, [lines, matches, at]);

  if (!pane) return null;
  const current = matches[Math.min(at, matches.length - 1)];
  return (
    <Dialog open onOpenChange={(o) => !o && close()}>
      <DialogContent className="flex h-[76vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-4xl" aria-label={`Scrollback of ${name || "the pane"}`}>
        <DialogTitle className="sr-only">Scrollback of {name || "the pane"}</DialogTitle>
        <DialogDescription className="sr-only">Search the pane's recent output</DialogDescription>
        <div className="flex items-center gap-3 border-b px-3 py-2.5 pr-12">
          <Search className="size-4 text-muted-foreground" />
          <input
            autoFocus
            type="search"
            placeholder="Search the scrollback…"
            aria-label="Search the scrollback"
            className="flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setAt(Number.MAX_SAFE_INTEGER);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter" && matches.length > 0) {
                e.preventDefault();
                const i = Math.min(at, matches.length - 1);
                setAt(e.shiftKey ? (i + 1) % matches.length : (i - 1 + matches.length) % matches.length);
              }
            }}
          />
          <span className="text-xs text-muted-foreground tabular-nums" role="status">
            {lines === undefined ? "Reading…" : query ? `${matches.length ? Math.min(at, matches.length - 1) + 1 : 0} of ${matches.length}` : `${lines.length} lines`}
          </span>
        </div>
        <div ref={list} className="min-h-0 flex-1 overflow-auto bg-term-bg px-3 py-2 font-mono text-[13px] leading-[1.3] whitespace-pre text-term-fg select-text">
          {(lines ?? []).map((l, i) => (
            <div key={i} data-line={i} className={cn(i === current ? "bg-primary text-primary-foreground" : matches.includes(i) && "bg-primary/20")}>
              {l || " "}
            </div>
          ))}
        </div>
        <p className="flex gap-3 border-t px-3 py-1.5 text-xs text-muted-foreground">
          <span>
            <Kbd>Enter</Kbd> previous match
          </span>
          <span>
            <Kbd>Shift</Kbd>+<Kbd>Enter</Kbd> next
          </span>
          <span>
            <Kbd>Esc</Kbd> close
          </span>
        </p>
      </DialogContent>
    </Dialog>
  );
}
