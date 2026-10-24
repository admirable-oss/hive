import { act, fireEvent, render, screen } from "@testing-library/react";
import type { Workspace } from "./api";

const calls: string[] = [];
vi.mock("./api", () => {
  const record = (name: string) => (...args: unknown[]) => {
    calls.push(`${name}(${args.join(",")})`);
    if (name === "branchDiff") {
      return Promise.resolve({
        branch: "hive/claude/3f2a91", base: "main", dirty: true, files: [{ path: "login.go", status: "M", added: 1, deleted: 1 }],
        patch: "diff --git a/login.go b/login.go\n--- a/login.go\n+++ b/login.go\n@@ -1,2 +1,2 @@\n package login\n-old\n+new\n",
      });
    }
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

  it("follows the [keys] prefix bindings, as hive ui does", () => {
    act(() =>
      store.set((s) => ({
        ...s,
        settings: {
          ...s.settings,
          prefix: ["ctrl+b"],
          keys: [
            { mode: "prefix", action: "split-right", keys: ["%"] },
            { mode: "prefix", action: "send-prefix", keys: ["ctrl+b"] },
          ],
        },
      })),
    );
    render(<App />);
    const input = screen.getByLabelText(/Type into/);
    fireEvent.keyDown(input, { key: "b", code: "KeyB", ctrlKey: true });
    expect(screen.getByText(/Prefix — press a key/)).toBeInTheDocument();
    expect(calls.some((c) => c.startsWith("input("))).toBe(false); // the prefix is not typed
    fireEvent.keyDown(input, { key: "%", code: "Digit5", shiftKey: true });
    expect(calls).toContain("splitPane(p1,right)");
    // The prefix pressed twice reaches the shell.
    fireEvent.keyDown(input, { key: "b", code: "KeyB", ctrlKey: true });
    fireEvent.keyDown(input, { key: "b", code: "KeyB", ctrlKey: true });
    expect(calls).toContain("input(p1,\x02)");
    act(() => store.set((s) => ({ ...s, settings: { ...s.settings, keys: [] } })));
  });

  it("starts an agent from the dialog", async () => {
    render(<App />);
    act(() => store.setUI({ dialog: { kind: "new-agent" } }));
    await screen.findByRole("option", { name: "Claude Code" });
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    expect(calls.some((c) => c.startsWith("startAgent("))).toBe(true);
  });
});

describe("Plans", () => {
  beforeEach(() => {
    calls.length = 0;
    act(() => {
      store.set((s) => ({ ...s, connection: { state: "connected", session: "default", version: "1.3.0", api_level: 12 } }));
      store.setWorkspace({
        ...ws,
        plans: [
          {
            id: "3f2a1b0c", name: "ship it", state: "running", total: 4, done: 1, counts: { succeeded: 1, running: 1, approval: 1, blocked: 1 },
            created: "", waiting: [{ id: "deploy", kind: "claude", state: "approval" }, { id: "tests", kind: "claude", state: "blocked", pane_id: "p1" }],
          },
          { id: "beta/9e8d7c6b", name: "", state: "failed", total: 2, done: 1, counts: { succeeded: 1, failed: 1 }, created: "", waiting: [], machine: "beta" },
        ],
      });
    });
  });

  it("shows plans, approves a task and goes to a blocked one", () => {
    render(<App />);
    const plans = screen.getByRole("region", { name: "Plans" });
    expect(plans).toHaveTextContent("ship it");
    expect(plans).toHaveTextContent("1/4 · 1 running · 1 blocked");
    expect(plans).toHaveTextContent("beta · 1/2 · 1 failed");
    fireEvent.click(screen.getByRole("button", { name: "Approve deploy" }));
    expect(calls).toContain("approveTask(3f2a1b0c,deploy,false)");
    fireEvent.click(screen.getByRole("button", { name: "Refuse deploy" }));
    expect(calls).toContain("approveTask(3f2a1b0c,deploy,true)");
    fireEvent.click(screen.getByRole("button", { name: /tests: its agent needs you/ }));
    expect(calls).toContain("focusPane(p1)");
    fireEvent.click(screen.getByRole("button", { name: "Cancel plan ship it" }));
    expect(calls).toContain("cancelPlan(3f2a1b0c)");
  });
});

describe("Branches", () => {
  beforeEach(() => {
    calls.length = 0;
    act(() => {
      store.set((s) => ({ ...s, connection: { state: "connected", session: "default", version: "1.4.0", api_level: 13 } }));
      store.setWorkspace({
        ...ws,
        branches: [
          {
            id: "fix-login", name: "hive/claude/3f2a91", base: "main", state: "working", environment_id: "fix-login",
            files: 1, added: 1, deleted: 1, ahead: 0, behind: 0, dirty: true, comments: 1, pending: 1,
            overlaps: [{ branch: "hive/codex/77aa01", files: ["login.go"], conflicts: ["login.go"] }],
            gates: [{ name: "test", passed: false, exit_code: 1, output: ["FAIL"], duration_ms: 1200 }],
          },
        ],
      });
    });
  });

  it("shows a branch's conflicts and reviews it", async () => {
    render(<App />);
    const branches = screen.getByRole("region", { name: "Branches" });
    expect(branches).toHaveTextContent("hive/claude/3f2a91");
    expect(branches).toHaveTextContent("conflicts with hive/codex/77aa01");
    fireEvent.click(screen.getByRole("button", { name: "Review hive/claude/3f2a91" }));
    expect(calls).toContain("branchDiff(fix-login)");
    expect(screen.getByRole("list", { name: "Overlaps" })).toHaveTextContent("would conflict in login.go");
    expect(screen.getByRole("list", { name: "Gates" })).toHaveTextContent("test: exit 1");

    // A comment on the added line goes to the agent.
    fireEvent.click(await screen.findByRole("button", { name: "+new" }));
    fireEvent.change(screen.getByPlaceholderText(/login.go:2/), { target: { value: "Explain this" } });
    fireEvent.click(screen.getByRole("button", { name: "Add and send" }));
    expect(calls).toContain("commentBranch(fix-login,login.go,2,Explain this,true)");
    await act(async () => {}); // the comment's call settles

    fireEvent.click(screen.getByRole("button", { name: /Run gates/ }));
    expect(calls).toContain("readyBranch(fix-login)");
    await act(async () => {});
    fireEvent.click(screen.getByRole("button", { name: /Send 1 comments/ }));
    expect(calls).toContain("sendComments(fix-login)");
  });
});

describe("Workflows", () => {
  beforeEach(() => {
    calls.length = 0;
    act(() => {
      store.set((s) => ({ ...s, connection: { state: "connected", session: "default", version: "1.5.0", api_level: 14 } }));
      store.setUI({ dialog: undefined, sidebar: true }); // the review an earlier test opened
      store.setWorkspace({
        ...ws,
        workflows: [
          { id: "77aa0011", workflow: "review", state: "running", trigger: "git", total: 4, done: 1, failed: 0, current: ["test"], created_at: "" },
          { id: "3f2a1b0c", workflow: "nightly", state: "failed", trigger: "schedule", total: 2, done: 2, failed: 1, current: [], reason: "step test failed: exit status 1", created_at: "" },
          ...["a1", "a2", "a3", "a4"].map((id) => ({ id, workflow: "old", state: "succeeded", trigger: "manual", total: 1, done: 1, failed: 0, current: [], created_at: "" })),
        ],
      });
    });
  });

  it("shows runs, cancels one going on and runs one again", () => {
    render(<App />);
    const runs = screen.getByRole("region", { name: "Workflows" });
    expect(runs).toHaveTextContent("review");
    expect(runs).toHaveTextContent("1/4 running · now test");
    expect(runs).toHaveTextContent("2/2 failed · 1 failed");
    expect(screen.getAllByRole("button", { name: /^Run a\d again$/ })).toHaveLength(2); // the latest finished, not all
    fireEvent.click(screen.getByRole("button", { name: "Cancel run 77aa0011" }));
    expect(calls).toContain("cancelWorkflowRun(77aa0011)");
    fireEvent.click(screen.getByRole("button", { name: "Run 3f2a1b0c again" }));
    expect(calls).toContain("rerunWorkflow(3f2a1b0c)");
  });
});
