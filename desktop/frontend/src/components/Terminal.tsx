import { memo, useEffect, useLayoutEffect, useRef } from "react";
import { Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { api, isMac, type Line, type Pane } from "../api";
import { encodeKey, encodeMouse, focusReport, ModeBracketedPaste, mouseWanted, type MouseKind } from "../keys";
import { lineText } from "../screen";
import { store, useImages, useScreen, useStore } from "../store";
import { color } from "../theme";
import { usePalette } from "./theme";

const ATTR_BOLD = 1,
  ATTR_FAINT = 2,
  ATTR_ITALIC = 4,
  ATTR_BLINK = 8,
  ATTR_REVERSE = 32,
  ATTR_CONCEAL = 64,
  ATTR_STRIKE = 128;

// Row draws one line: each run takes exactly its columns, so wide
// characters and odd glyphs never shift the rest of the line.
const Row = memo(function Row({ line, pal }: { line: Line; pal: string[] }) {
  return (
    <div className="term-row">
      {line.runs.map((r, i) => {
        const attrs = r.a ?? 0;
        let fg = color(r.f, pal);
        let bg = color(r.b, pal);
        if (attrs & ATTR_REVERSE) [fg, bg] = [bg ?? "var(--term-bg)", fg ?? "var(--term-fg)"];
        const underline = attrs >> 8;
        const style: React.CSSProperties = {
          width: `${r.w}ch`,
          color: attrs & ATTR_CONCEAL ? "transparent" : fg,
          background: bg,
          fontWeight: attrs & ATTR_BOLD ? 700 : undefined,
          opacity: attrs & ATTR_FAINT ? 0.6 : undefined,
          fontStyle: attrs & ATTR_ITALIC ? "italic" : undefined,
          textDecorationLine:
            [underline ? "underline" : "", attrs & ATTR_STRIKE ? "line-through" : ""].filter(Boolean).join(" ") || undefined,
          textDecorationStyle: underline === 3 ? "wavy" : underline === 2 ? "double" : underline === 4 ? "dotted" : underline === 5 ? "dashed" : undefined,
        };
        const cls = attrs & ATTR_BLINK ? "term-run term-blink" : "term-run";
        if (r.l) {
          return (
            <a
              key={i}
              className={cls + " cursor-pointer underline decoration-dotted underline-offset-2"}
              style={style}
              href={r.l}
              title={r.l}
              onClick={(e) => {
                e.preventDefault();
                api.openLink(r.l!).catch(store.fail);
              }}
            >
              {r.t}
            </a>
          );
        }
        return (
          <span key={i} className={cls} style={style}>
            {r.t}
          </span>
        );
      })}
    </div>
  );
});

interface Props {
  pane: Pane;
  cols: number;
  rows: number;
  focused: boolean;
  onFocus: () => void;
  // cell is the size of one character cell, for mouse reports.
  cell: { w: number; h: number };
}

// Terminal shows a pane's screen and types into it.
export function Terminal({ pane, cols, rows, focused, onFocus, cell }: Props) {
  const screen = useScreen(pane.id);
  const images = useImages(pane.id);
  const pal = usePalette();
  const input = useRef<HTMLTextAreaElement>(null);
  const composing = useRef(false);
  const mac = isMac();
  const optionIsMeta = useStore((s) => s.ui.optionIsMeta);
  const modes = screen?.modes ?? 0;
  const lastMove = useRef("");

  useEffect(() => {
    if (cols >= 2 && rows >= 1) api.openView(pane.id, cols, rows).catch(store.fail);
  }, [pane.id, pane.process_id, cols, rows]);
  useEffect(() => () => void api.closeView(pane.id).catch(() => {}), [pane.id]);

  useLayoutEffect(() => {
    if (focused && document.activeElement !== input.current && !window.getSelection()?.toString()) {
      input.current?.focus({ preventScroll: true });
    }
  }, [focused]);

  const send = (data: string | null) => {
    if (data) api.input(pane.id, data).catch(store.fail);
  };

  // Programs that ask for focus events (editors, some agents) hear when
  // this pane gains or loses focus.
  const reportedFocus = useRef<boolean | null>(null);
  useEffect(() => {
    if (reportedFocus.current === focused) return;
    reportedFocus.current = focused;
    const r = focusReport(focused, modes);
    if (r) api.input(pane.id, r).catch(() => {});
  }, [focused, modes, pane.id]);

  // Mouse reports go to programs that ask for them; Shift keeps the
  // mouse for selecting text, as terminals do.
  const reporting = mouseWanted(modes);
  const mouse = (kind: MouseKind, e: React.MouseEvent, button = e.button) => {
    if (!reporting || e.shiftKey) return false;
    const r = e.currentTarget.getBoundingClientRect();
    const x = Math.max(0, Math.min(cols - 1, Math.floor((e.clientX - r.left) / cell.w)));
    const y = Math.max(0, Math.min(rows - 1, Math.floor((e.clientY - r.top) / cell.h)));
    if (kind === "move") {
      const key = `${x},${y},${e.buttons}`;
      if (key === lastMove.current) return true; // the same cell: nothing new
      lastMove.current = key;
    }
    const report = encodeMouse(
      {
        kind,
        button,
        x,
        y,
        shift: e.shiftKey,
        alt: e.altKey,
        ctrl: e.ctrlKey,
        buttons: e.buttons,
      },
      modes,
    );
    send(report);
    return true;
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (composing.current || e.nativeEvent.isComposing) return;
    if (!mac && e.ctrlKey && e.shiftKey && (e.key === "C" || e.key === "c")) {
      e.preventDefault();
      const text = window.getSelection()?.toString();
      if (text) api.copy(text).catch(store.fail);
      return;
    }
    if (!mac && e.ctrlKey && e.shiftKey && (e.key === "V" || e.key === "v")) {
      e.preventDefault();
      api.pasteClipboard(pane.id).catch(store.fail);
      return;
    }
    if ((mac ? e.metaKey : e.ctrlKey && e.shiftKey) && (e.key === "f" || e.key === "F")) {
      e.preventDefault();
      store.setUI({ history: pane.id });
      return;
    }
    const bytes = encodeKey(e, modes, mac, optionIsMeta);
    if (bytes !== null) {
      e.preventDefault();
      send(bytes);
    }
  };

  const onInput = (e: React.FormEvent<HTMLTextAreaElement>) => {
    if (composing.current) return;
    const el = e.currentTarget;
    send(el.value.replace(/\n/g, "\r"));
    el.value = "";
  };

  const onPaste = (e: React.ClipboardEvent) => {
    e.preventDefault();
    const text = e.clipboardData.getData("text/plain");
    if (text) api.paste(pane.id, text).catch(store.fail);
  };

  const cursor = screen?.cursor;
  const title = pane.name || screen?.title || "terminal";
  const bracketed = ((screen?.modes ?? 0) & ModeBracketedPaste) !== 0;
  return (
    <div
      className={cn(
        "relative h-full w-full cursor-text overflow-hidden bg-term-bg text-term-fg transition-shadow",
        focused && "shadow-[inset_0_0_0_1px_var(--ring)]",
      )}
      onMouseUp={() => {
        if (!window.getSelection()?.toString()) {
          onFocus();
          input.current?.focus({ preventScroll: true });
        }
      }}
      onPaste={onPaste}
      data-pane={pane.id}
      data-bracketed={bracketed || undefined}
    >
      <div
        className={cn("term-screen relative select-text", reporting && "cursor-default")}
        role="log"
        aria-label={`Terminal: ${title}`}
        aria-live="off"
        onMouseDown={(e) => {
          if (mouse("press", e)) {
            e.preventDefault();
            onFocus();
            input.current?.focus({ preventScroll: true });
          }
        }}
        onMouseUp={(e) => void mouse("release", e)}
        onMouseMove={(e) => void mouse("move", e)}
        onWheel={(e) => {
          if (reporting && !e.shiftKey) mouse(e.deltaY < 0 ? "wheel-up" : "wheel-down", e, 0);
        }}
        onContextMenu={(e) => {
          if (reporting && !e.shiftKey) e.preventDefault();
        }}
      >
        {(screen?.lines ?? []).map((l, y) => (
          <Row key={y} line={l} pal={pal} />
        ))}
        {(images ?? []).map((img) => (
          <img
            key={img.key}
            className="term-image pointer-events-none absolute object-contain"
            src={img.src}
            alt=""
            draggable={false}
            style={{ left: `${img.x}ch`, top: `calc(${img.y} * var(--cell-h))`, width: `${img.cols}ch`, height: `calc(${img.rows} * var(--cell-h))` }}
          />
        ))}
        {cursor?.visible && focused && !screen?.ended && (
          <div
            className={`term-cursor ${cursor.shape}${cursor.blink ? " term-blink" : ""}`}
            style={{ left: `${cursor.x}ch`, top: `calc(${cursor.y} * var(--cell-h))` }}
            aria-hidden
          />
        )}
      </div>
      {!screen && (
        <div className="absolute inset-0 grid place-items-center text-xs text-muted-foreground">
          <span className="flex items-center gap-2">
            <Loader2 className="size-3.5 animate-spin" /> Connecting to the terminal…
          </span>
        </div>
      )}
      {screen?.ended && !pane.running && (
        <div className="absolute right-2 bottom-2 rounded-md border bg-card px-2 py-1 text-xs text-muted-foreground shadow-sm" role="status">
          Process ended{pane.exit != null ? ` (exit ${pane.exit})` : ""}
        </div>
      )}
      <textarea
        ref={input}
        className="absolute top-0 left-0 size-px resize-none overflow-hidden border-0 p-0 opacity-0"
        aria-label={`Type into ${title}`}
        autoCapitalize="off"
        autoComplete="off"
        autoCorrect="off"
        spellCheck={false}
        onKeyDown={onKeyDown}
        onInput={onInput}
        onCompositionStart={() => (composing.current = true)}
        onCompositionEnd={(e) => {
          composing.current = false;
          send(e.data);
          e.currentTarget.value = "";
        }}
      />
    </div>
  );
}

// screenText is a pane's visible text, for the palette's previews.
export function screenText(pane: string): string {
  return (store.screen(pane)?.lines ?? []).map(lineText).join("\n");
}
