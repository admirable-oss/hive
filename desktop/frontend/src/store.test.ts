import type { Workspace } from "./api";
import { attention, currentPane, currentTab, emptyWorkspace, Store } from "./store";

const ws: Workspace = {
  ...emptyWorkspace,
  environments: [
    { id: "api", name: "api", path: "/code/api" },
    { id: "web", name: "web", path: "/code/web" },
  ],
  tabs: [
    { id: "t1", environment_id: "api", name: "one", width: 80, height: 24 },
    { id: "t2", environment_id: "api", name: "two", width: 80, height: 24, active: true, focused: "p3" },
    { id: "t3", environment_id: "web", name: "three", width: 80, height: 24 },
  ],
  panes: [
    { id: "p1", tab_id: "t1", environment_id: "api", process_id: "x1", running: true },
    { id: "p2", tab_id: "t2", environment_id: "api", process_id: "x2", running: true },
    { id: "p3", tab_id: "t2", environment_id: "api", process_id: "x3", running: true },
    { id: "p4", tab_id: "t3", environment_id: "web", process_id: "x4", running: true },
  ],
  agents: [
    { id: "a1", kind: "claude", name: "claude", environment_id: "api", pane_id: "p2", state: "done", since: "", state_seq: 3 },
    { id: "a2", kind: "codex", name: "codex", environment_id: "web", pane_id: "p4", state: "blocked", since: "", state_seq: 2 },
  ],
};

describe("store", () => {
  it("shows the environment's active tab and its focused pane", () => {
    const s = new Store();
    s.setWorkspace(ws);
    expect(currentTab(s.state)?.id).toBe("t2");
    expect(currentPane(s.state)?.id).toBe("p3");
    s.setUI({ env: "web" });
    expect(currentTab(s.state)?.id).toBe("t3");
  });

  it("reveals a pane once the workspace has it", () => {
    const s = new Store();
    s.setWorkspace({ ...ws, panes: ws.panes.filter((p) => p.id !== "p4") });
    s.reveal("p4");
    expect(currentPane(s.state)?.id).not.toBe("p4");
    s.setWorkspace(ws);
    expect(currentTab(s.state)?.id).toBe("t3");
    expect(currentPane(s.state)?.id).toBe("p4");
  });

  it("orders agents needing someone: blocked first", () => {
    expect(attention(ws).map((a) => a.id)).toEqual(["a2", "a1"]);
  });

  it("asks again for a pane whose updates were lost", () => {
    const s = new Store();
    const lost: string[] = [];
    s.onLost = (p) => lost.push(p);
    const base = { pane: "p1", cols: 4, rows: 1, cursor: { x: 0, y: 0, visible: true, shape: "block" }, modes: 0, lines: [] };
    s.applyScreen({ ...base, seq: 1, full: true });
    s.applyScreen({ ...base, seq: 3, full: false });
    expect(lost).toEqual(["p1"]);
  });
});

describe("images", () => {
  it("places, replaces, deletes and clears", () => {
    const s = new Store();
    let calls = 0;
    s.subscribeScreen("p1", () => calls++);
    const img = (key: string, image: number) => ({ pane: "p1", op: "place", key, image, x: 0, y: 0, cols: 2, rows: 1, src: "data:image/png;base64,AA" });
    s.applyImage(img("7:1", 7));
    s.applyImage(img("7:1", 7));
    s.applyImage(img("8:1", 8));
    expect(s.imagesOf("p1")?.map((i) => i.key)).toEqual(["7:1", "8:1"]);
    s.applyImage({ pane: "p1", op: "delete", image: 7, x: 0, y: 0, cols: 0, rows: 0 });
    expect(s.imagesOf("p1")?.map((i) => i.key)).toEqual(["8:1"]);
    s.applyImage({ pane: "p1", op: "clear", x: 0, y: 0, cols: 0, rows: 0 });
    expect(s.imagesOf("p1")).toEqual([]);
    expect(calls).toBe(5);
    for (let i = 0; i < 40; i++) s.applyImage(img(`${i}:1`, i));
    expect(s.imagesOf("p1")).toHaveLength(32);
  });
});
