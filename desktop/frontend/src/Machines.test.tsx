import { act, fireEvent, render, screen } from "@testing-library/react";

const calls: string[] = [];
const id = "q3xdk2abfj4ldtxk5nqbbmvy7a";
const check = (trust: string) => ({
  machine: { name: "studio", host: "192.168.1.7" },
  proof: { id, name: "studio", hostname: "studio.lan", platform: "linux/amd64", version: "1.0.9" },
  fingerprint: "SHA256:abc",
  trust,
  pinned: trust === "changed" ? "SHA256:old" : undefined,
});
const answers: Record<string, unknown> = {
  discover: { runtimes: [{ name: "studio", id, addresses: ["192.168.1.7"], port: 22, state: "discovered" }], interfaces: ["en0"], errors: [] },
  checkPair: check("new"),
  checkTrust: check("changed"),
};
vi.mock("./api", () => ({
  api: new Proxy(
    {},
    {
      get: (_t, name: string) =>
        (...args: unknown[]) => {
          calls.push(`${name}(${args.map((a) => JSON.stringify(a)).join(",")})`);
          return Promise.resolve(answers[name] ?? "");
        },
    },
  ),
  listen: () => () => {},
  isMac: () => false,
}));

const { Dialogs } = await import("./components/Dialogs");
const { store } = await import("./store");

describe("machines", () => {
  beforeEach(() => {
    calls.length = 0;
  });

  it("pairs only the identity it showed", async () => {
    render(<Dialogs />);
    act(() => store.setUI({ dialog: { kind: "machines" } }));
    fireEvent.click(screen.getByRole("button", { name: "Find machines nearby" }));
    fireEvent.click(await screen.findByRole("button", { name: "Pair…" }));
    expect(await screen.findByText("SHA256:abc")).toBeInTheDocument();
    expect(calls.some((c) => c.startsWith("pair("))).toBe(false); // nothing saved yet
    fireEvent.click(screen.getByRole("button", { name: "Trust and save as studio" }));
    expect(calls).toContain(`pair({"id":"${id}"},"${id}")`);
  });

  it("shows a changed identity, old and new, before trusting it", async () => {
    act(() =>
      store.set((s) => ({
        ...s,
        machines: [{ name: "studio", host: "192.168.1.7", state: "changed", identity_changed: true, connection: { state: "off", session: "studio" }, counts: {} }],
      })),
    );
    render(<Dialogs />);
    act(() => store.setUI({ dialog: { kind: "machines" } }));
    fireEvent.click(screen.getByRole("button", { name: "Review identity…" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("different identity");
    expect(screen.getByText("SHA256:old")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Trust the new identity" }));
    expect(calls).toContain(`trust("studio","${id}")`);
  });
});
