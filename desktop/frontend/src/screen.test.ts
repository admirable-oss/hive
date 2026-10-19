import type { ScreenUpdate } from "./api";
import { applyUpdate, lineText } from "./screen";

const u = (over: Partial<ScreenUpdate>): ScreenUpdate => ({
  pane: "p1",
  seq: 1,
  full: true,
  cols: 10,
  rows: 3,
  cursor: { x: 0, y: 0, visible: true, shape: "block" },
  modes: 0,
  lines: [],
  ...over,
});

const line = (y: number, t: string) => ({ y, runs: [{ t, w: t.length }] });

describe("applyUpdate", () => {
  it("builds the screen from a full update and patches lines", () => {
    let { screen, lost } = applyUpdate(undefined, u({ lines: [line(0, "hello"), line(2, "$")] }));
    expect(lost).toBe(false);
    expect(screen.lines.map(lineText)).toEqual(["hello", "", "$"]);
    ({ screen, lost } = applyUpdate(screen, u({ seq: 2, full: false, lines: [line(1, "world")] })));
    expect(lost).toBe(false);
    expect(screen.lines.map(lineText)).toEqual(["hello", "world", "$"]);
  });

  it("asks again when an update was skipped, and keeps the screen", () => {
    const first = applyUpdate(undefined, u({ lines: [line(0, "a")] })).screen;
    const { screen, lost } = applyUpdate(first, u({ seq: 5, full: false, lines: [line(0, "b")] }));
    expect(lost).toBe(true);
    expect(lineText(screen.lines[0])).toBe("a");
  });

  it("resizes with the terminal", () => {
    const first = applyUpdate(undefined, u({ lines: [line(0, "a")] })).screen;
    const { screen } = applyUpdate(first, u({ seq: 2, rows: 5, lines: [line(4, "z")] }));
    expect(screen.rows).toBe(5);
    expect(screen.lines).toHaveLength(5);
    expect(lineText(screen.lines[4])).toBe("z");
  });

  it("marks the end of a process", () => {
    const first = applyUpdate(undefined, u({})).screen;
    const { screen } = applyUpdate(first, u({ seq: 2, full: false, ended: true }));
    expect(screen.ended).toBe(true);
  });
});
