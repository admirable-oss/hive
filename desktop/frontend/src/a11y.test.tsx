import { act, render } from "@testing-library/react";
import axe from "axe-core";
import type { Workspace } from "./api";

vi.mock("./api", () => ({
  api: new Proxy({}, { get: () => () => Promise.resolve([]) }),
  listen: () => () => {},
  isMac: () => false,
  isWindows: () => false,
}));

const { App } = await import("./components/App");
const { store } = await import("./store");

const ws: Workspace = {
  environments: [{ id: "api", name: "api", path: "/code/api", state: "blocked", branch: "main" }],
  tabs: [{ id: "t1", environment_id: "api", name: "fix login", width: 80, height: 24, active: true, focused: "p1", state: "blocked" }],
  panes: [{ id: "p1", tab_id: "t1", environment_id: "api", process_id: "x1", running: true, rect: { x: 0, y: 0, w: 80, h: 24 }, agent: "a1" }],
  agents: [{ id: "a1", kind: "claude", name: "claude", environment_id: "api", pane_id: "p1", tab_id: "t1", state: "blocked", since: "", state_seq: 2 }],
  state: "blocked",
  counts: { blocked: 1 },
};

// jsdom computes no styles: contrast is checked in a real browser by the
// end-to-end test instead.
async function violations(el: Element) {
  const r = await axe.run(el, { rules: { "color-contrast": { enabled: false } } });
  return r.violations.filter((v) => v.impact === "serious" || v.impact === "critical").map((v) => `${v.id}: ${v.help}`);
}

describe("accessibility", () => {
  beforeEach(() => {
    act(() => {
      store.set((s) => ({
        ...s,
        connection: { state: "connected", session: "default" },
        machines: [{ name: "gpu", host: "gpu", state: "changed", identity_changed: true, connection: { state: "off", session: "gpu" }, counts: {} }],
      }));
      store.setWorkspace(ws);
    });
  });

  it("the workspace", async () => {
    const { container } = render(<App />);
    expect(await violations(container)).toEqual([]);
  });

  it("the palette and the dialogs", async () => {
    const { container } = render(<App />);
    for (const ui of [{ palette: true }, { palette: false, dialog: { kind: "machines" as const } }, { dialog: { kind: "new-environment" as const } }]) {
      act(() => store.setUI(ui));
      expect(await violations(container)).toEqual([]);
    }
  });
});
