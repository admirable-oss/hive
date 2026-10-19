import type { CursorState, Line, ScreenUpdate } from "./api";

// Screen is a pane's screen as the frontend draws it: one Line per row.
export interface Screen {
  cols: number;
  rows: number;
  lines: Line[];
  cursor: CursorState;
  modes: number;
  title: string;
  seq: number;
  ended: boolean;
}

export const emptyScreen: Screen = {
  cols: 0,
  rows: 0,
  lines: [],
  cursor: { x: 0, y: 0, visible: false, shape: "block" },
  modes: 0,
  title: "",
  seq: 0,
  ended: false,
};

const blank = (y: number): Line => ({ y, runs: [] });

// applyUpdate returns the screen after an update, and whether lines were
// lost (an update skipped): the caller then asks for the whole screen.
export function applyUpdate(prev: Screen | undefined, u: ScreenUpdate): { screen: Screen; lost: boolean } {
  const base = prev ?? emptyScreen;
  if (!u.full && u.seq !== 0 && base.seq !== 0 && u.seq !== base.seq + 1) {
    // Out of order or skipped: keep what we have, ask again.
    const lost = u.seq > base.seq + 1;
    return { screen: { ...base, ended: base.ended || !!u.ended }, lost };
  }
  if (u.ended && !u.full && (u.lines ?? []).length === 0 && u.rows === 0) {
    return { screen: { ...base, ended: true }, lost: false };
  }
  const rows = u.rows;
  let lines: Line[];
  if (u.full || !prev || prev.rows !== rows) {
    lines = Array.from({ length: rows }, (_, y) => (u.full ? blank(y) : (base.lines[y] ?? blank(y))));
  } else {
    lines = base.lines.slice();
  }
  for (const l of u.lines ?? []) {
    if (l.y >= 0 && l.y < rows) lines[l.y] = l;
  }
  return {
    screen: {
      cols: u.cols,
      rows,
      lines,
      cursor: u.cursor,
      modes: u.modes,
      title: u.title ?? "",
      seq: u.seq,
      ended: !!u.ended,
    },
    lost: !prev && !u.full,
  };
}

// lineText is a line's text (for tests and accessibility).
export function lineText(l: Line | undefined): string {
  return (l?.runs ?? []).map((r) => r.t).join("");
}
