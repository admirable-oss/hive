import { act, fireEvent, render, screen } from "@testing-library/react";
import type { Workspace } from "./api";

const calls: string[] = [];
vi.mock("./api", () => {
  const record = (name: string) => (...args: unknown[]) => {
    calls.push(`${name}(${args.join(",")})`);
    return Promise.resolve(name === "manifests" ? [{ id: "claude", name: "Claude Code" }] : "");
  };
  return {
    api: new Proxy({}, { get: (_t, name: string) => record(name) }),
    listen: () => () => {},
    isMac: () => false,
  };
});

const { App } = await import("./components/App");
const { store } = await import("./store");

const ws: Workspace = {
  environments: [{ id: "api", name: "api", path: "/code/api", state: "blocked" }],
  tabs: [{ id: "t1", environment_id: "api", name: "fix login", width: 80, height: 24, active: true, focused: "p1", state: "blocked" }],
  panes: [{ id: "p1", tab_id: "t1", environment_id: "api", process_id: "x1", running: true, rect: { x: 0, y: 0, w: 80, h: 24 }, agent: "a1" }],
  agents: [{ id: "a1", kind: "claude", name: "claude", environment_id: "api", pane_id: "p1", tab_id: "t1", state: "blocked", since: "", state_seq: 2, reason: "asks to run a command" }],
  state: "blocked",
  counts: { blocked: 1 },
};

describe("App", () => {
  beforeEach(() => {
    calls.length = 0;
    act(() => {
      store.set((s) => ({ ...s, connection: { state: "connected", session: "default", version: "1.0.9", api_level: 11 } }));
      store.setWorkspace(ws);
    });
  });

  it("lists agents that need someone and shows their pane", () => {
    render(<App />);
    const needs = screen.getByRole("region", { name: "Needs you" });
    expect(needs).toHaveTextContent("claude");
    expect(screen.getByRole("tab", { name: /fix login/ })).toHaveAttribute("aria-selected", "true");
    fireEvent.click(screen.getAllByRole("button", { name: /claude/ })[0]);
    expect(calls).toContain("focusPane(p1)");
  });

  it("says when the daemon cannot be reached", () => {
    act(() => store.set((s) => ({ ...s, connection: { state: "offline", session: "default", message: "connection refused" } })));
    render(<App />);
    expect(screen.getByRole("alert")).toHaveTextContent("connection refused");
    fireEvent.click(screen.getByRole("button", { name: "Retry now" }));
    expect(calls).toContain("retry()");
  });

  it("opens the palette and goes to an agent", () => {
    render(<App />);
    act(() => store.setUI({ palette: true }));
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "clau" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(calls).toContain("focusPane(p1)");
    expect(store.state.ui.palette).toBe(false);
  });

  it("types into the focused pane", () => {
    render(<App />);
    const input = screen.getByLabelText(/Type into/);
    fireEvent.keyDown(input, { key: "Enter" });
    fireEvent.keyDown(input, { key: "c", ctrlKey: true });
    expect(calls).toContain("input(p1,\r)");
    expect(calls).toContain("input(p1,\x03)");
  });

  it("starts an agent from the dialog", async () => {
    render(<App />);
    act(() => store.setUI({ dialog: { kind: "new-agent" } }));
    await screen.findByRole("option", { name: "Claude Code" });
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    expect(calls.some((c) => c.startsWith("startAgent("))).toBe(true);
  });
});
