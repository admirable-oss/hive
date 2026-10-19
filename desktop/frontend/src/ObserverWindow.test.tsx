import { act, render, screen, waitFor } from "@testing-library/react";
import axe from "axe-core";
import type { ObservedScreen, ObservedView } from "./api";

const calls: string[] = [];
let pushView: (v: ObservedView) => void = () => {};
let pushScreen: (s: ObservedScreen) => void = () => {};
const view: ObservedView = {
  observer: { id: "o1", name: "desktop window", scope: "everything", screens: true, expires_at: "2026-10-20T10:00:00Z" },
  state: "connected",
  workspace: {
    environments: [{ id: "api", name: "api", path: "/code/api" }],
    tabs: [],
    panes: [
      { id: "p1", tab_id: "t1", environment_id: "api", process_id: "x1", running: true, agent: "a1" },
      { id: "p2", tab_id: "t1", environment_id: "api", process_id: "x2", running: true },
    ],
    agents: [{ id: "a1", kind: "claude", name: "claude", environment_id: "api", pane_id: "p1", state: "blocked", since: "", state_seq: 1, reason: "asks to run tests" }],
    state: "blocked",
    counts: { blocked: 1 },
  },
};

vi.mock("./api", () => ({
  observerApi: {
    view: () => Promise.resolve(view),
    watch: (p: string) => {
      calls.push(`watch(${p})`);
      return Promise.resolve();
    },
    unwatch: (p: string) => {
      calls.push(`unwatch(${p})`);
      return Promise.resolve();
    },
  },
  listenObserved: (v: typeof pushView, s: typeof pushScreen) => {
    pushView = v;
    pushScreen = s;
    return () => {};
  },
  isMac: () => false,
}));

const { ObserverWindow } = await import("./components/ObserverWindow");

describe("observer window", () => {
  it("shows agents and their screens, and has no way to type", async () => {
    const { container } = render(<ObserverWindow />);
    expect(await screen.findByText("asks to run tests")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Read-only");
    await waitFor(() => expect(calls).toContain("watch(p1)")); // an effect, which may run after the text shows
    expect(calls).not.toContain("watch(p2)"); // shells only when asked
    act(() => pushScreen({ pane: "p1", keyframe: true, width: 20, height: 2, lines: [{ y: 0, text: "Run the tests? y/n" }], cursor: { x: 0, y: 0, visible: true, shape: "block" } }));
    expect(screen.getByLabelText("Screen")).toHaveTextContent("Run the tests? y/n");
    expect(container.querySelectorAll("textarea, input[type=text], input:not([type])").length).toBe(0);
    const r = await axe.run(container, { rules: { "color-contrast": { enabled: false } } });
    expect(r.violations.filter((v) => v.impact === "serious" || v.impact === "critical").map((v) => v.id)).toEqual([]);
  });

  it("says when its access ended", async () => {
    render(<ObserverWindow />);
    await screen.findByText("asks to run tests");
    act(() => pushView({ ...view, state: "ended", message: "this window's access ended (revoked or expired)" }));
    expect(screen.getByRole("alert")).toHaveTextContent("access ended");
  });
});
