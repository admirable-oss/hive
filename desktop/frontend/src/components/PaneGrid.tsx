import { GripVertical } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { cn } from "@/lib/utils";
import { api, type Pane, type Tab } from "../api";
import { currentPane, store, useStore } from "../store";
import { Terminal } from "./Terminal";

export interface CellSize {
  w: number;
  h: number;
}

// useCellSize measures one cell of the terminal font.
export function useCellSize(): [React.RefObject<HTMLSpanElement | null>, CellSize] {
  const probe = useRef<HTMLSpanElement>(null);
  const [size, setSize] = useState<CellSize>({ w: 8, h: 17 });
  useLayoutEffect(() => {
    const el = probe.current;
    if (!el) return;
    const measure = () => {
      const r = el.getBoundingClientRect();
      if (r.width > 0 && r.height > 0) setSize({ w: r.width / 100, h: r.height });
    };
    measure();
    document.fonts?.ready.then(measure);
  }, []);
  return [probe, size];
}

// useArea is an element's size in whole cells.
function useArea(cell: CellSize): [React.RefObject<HTMLDivElement | null>, { cols: number; rows: number; w: number; h: number }] {
  const ref = useRef<HTMLDivElement>(null);
  const [area, setArea] = useState({ cols: 0, rows: 0, w: 0, h: 0 });
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const update = () => {
      const { width, height } = el.getBoundingClientRect();
      setArea({ cols: Math.floor(width / cell.w), rows: Math.floor(height / cell.h), w: width, h: height });
    };
    update();
    const ro = new ResizeObserver(update);
    ro.observe(el);
    return () => ro.disconnect();
  }, [cell.w, cell.h]);
  return [ref, area];
}

// PaneGrid lays out a tab's panes where the daemon put them, scaled to the
// room the window has, and claims that room for the tab.
export function PaneGrid({ tab }: { tab: Tab }) {
  const panes = useStore((s) => s.workspace.panes).filter((p) => p.tab_id === tab.id);
  const focused = useStore((s) => currentPane(s)?.id);
  const [probe, cell] = useCellSize();
  const [ref, area] = useArea(cell);

  // Claim the area for the tab, once it has settled.
  useEffect(() => {
    if (area.cols < 2 || area.rows < 1 || (area.cols === tab.width && area.rows === tab.height)) return;
    const t = setTimeout(() => api.resizeTab(tab.id, area.cols, area.rows).catch(store.fail), 120);
    return () => clearTimeout(t);
  }, [tab.id, tab.width, tab.height, area.cols, area.rows]);

  const sx = tab.width > 0 ? area.w / tab.width : cell.w;
  const sy = tab.height > 0 ? area.h / tab.height : cell.h;
  const tiled = panes.filter((p) => p.rect && !p.popup);
  const popups = panes.filter((p) => p.popup && p.rect);
  const focus = (p: Pane) => {
    store.setUI({ pane: p.id });
    if (!p.focused) api.focusPane(p.id).catch(store.fail);
  };
  const cellVars = { "--cell-h": `${cell.h}px` } as React.CSSProperties;
  // Dragging a pane by its grip onto another swaps the two, through the
  // daemon's layout API (pane.swap).
  const [dragging, setDragging] = useState<string>();
  const [over, setOver] = useState<string>();
  const movable = tiled.length > 1;
  const drop = (target: string) => {
    if (dragging && dragging !== target) api.swapPane(dragging, target).catch(store.fail);
    setDragging(undefined);
    setOver(undefined);
  };

  return (
    <div className="flex min-h-0 flex-1 bg-term-bg p-1.5">
    <div className="relative min-h-0 flex-1 overflow-hidden bg-border font-mono text-[length:var(--term-size)] leading-[var(--term-line)]" ref={ref} style={cellVars}>
      <span className="cell-probe invisible absolute whitespace-pre" ref={probe} aria-hidden>
        {"0".repeat(100)}
      </span>
      {area.w > 0 &&
        [...tiled, ...popups].map((p) => {
          const r = p.rect!;
          return (
            <section
              key={p.id}
              data-pane={p.id}
              data-focused={p.id === focused || undefined}
              data-drop-target={(over === p.id && dragging !== p.id) || undefined}
              className={cn(
                "group/pane absolute overflow-hidden pb-px pl-px",
                p.popup && "z-10 rounded-lg shadow-2xl ring-1 ring-border",
                over === p.id && dragging !== p.id && "ring-2 ring-primary ring-inset",
              )}
              style={{ left: r.x * sx, top: r.y * sy, width: r.w * sx, height: r.h * sy }}
              aria-label={p.name || "pane"}
              onDragOver={(e) => {
                if (!dragging || dragging === p.id || p.popup) return;
                e.preventDefault();
                e.dataTransfer.dropEffect = "move";
                setOver(p.id);
              }}
              onDragLeave={() => setOver((o) => (o === p.id ? undefined : o))}
              onDrop={(e) => {
                e.preventDefault();
                drop(p.id);
              }}
            >
              <Terminal pane={p} cols={r.w} rows={r.h} cell={cell} focused={p.id === focused} onFocus={() => focus(p)} />
              {movable && !p.popup && (
                <button
                  type="button"
                  draggable
                  className="absolute top-1 left-1 z-20 rounded-sm bg-card/80 p-0.5 text-muted-foreground opacity-0 shadow-sm transition-opacity group-hover/pane:opacity-100 focus-visible:opacity-100 cursor-grab active:cursor-grabbing"
                  aria-label={`Move ${p.name || "pane"}: drag it onto another pane to swap them`}
                  title="Drag onto another pane to swap them"
                  onDragStart={(e) => {
                    e.dataTransfer.setData("application/x-hive-pane", p.id);
                    e.dataTransfer.effectAllowed = "move";
                    setDragging(p.id);
                  }}
                  onDragEnd={() => {
                    setDragging(undefined);
                    setOver(undefined);
                  }}
                >
                  <GripVertical className="size-3.5" />
                </button>
              )}
            </section>
          );
        })}
      {tiled.length === 0 && <div className="grid h-full place-items-center bg-background text-sm text-muted-foreground">This tab has no panes.</div>}
    </div>
    </div>
  );
}

// SinglePane fills a window with one pane (a pane popped out of the main
// window), sized to the window.
export function SinglePane({ pane }: { pane: Pane }) {
  const [probe, cell] = useCellSize();
  const [ref, area] = useArea(cell);
  return (
    <div className="flex min-h-0 flex-1 bg-term-bg p-1.5">
    <div className="relative min-h-0 flex-1 overflow-hidden font-mono text-[length:var(--term-size)] leading-[var(--term-line)]" ref={ref} style={{ "--cell-h": `${cell.h}px` } as React.CSSProperties}>
      <span className="cell-probe invisible absolute whitespace-pre" ref={probe} aria-hidden>
        {"0".repeat(100)}
      </span>
      {area.cols > 1 && (
        <section className="absolute inset-0" data-pane={pane.id} data-focused>
          <Terminal pane={pane} cols={area.cols} rows={area.rows} cell={cell} focused onFocus={() => {}} />
        </section>
      )}
    </div>
    </div>
  );
}
