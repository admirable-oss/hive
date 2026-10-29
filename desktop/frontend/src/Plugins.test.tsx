import { act, render, screen } from "@testing-library/react";

const plugins = [
  { id: "auto-tab-title", name: "Automatic tab titles", version: "0.2.0", tier: "official", tier_meaning: "maintained by Hive's maintainers", publisher: "hive", signed_by: "SHA256:off1", sandbox: "sandbox-exec", enabled: true, state: "ready", registry: true, checked_at: "" },
  { id: "pr-status", name: "PR status", version: "0.1.0", tier: "community", tier_meaning: "automated checks only", publisher: "acme", sandbox: "none", enabled: true, state: "ready", registry: true, revoked: "leaks the GitHub token", advisory: "https://example.org/advisories/1", checked_at: "" },
  { id: "local", name: "Local", version: "0.0.1", tier: "unlisted", tier_meaning: "installed from source, not from the registry", sandbox: "none", enabled: false, state: "", registry: false, checked_at: "" },
];
vi.mock("./api", () => ({
  api: new Proxy({}, { get: (_t, name: string) => () => Promise.resolve(name === "plugins" ? plugins : "") }),
  listen: () => () => {},
  isMac: () => false,
}));

const { Dialogs } = await import("./components/Dialogs");
const { store } = await import("./store");

describe("plugins", () => {
  it("shows each plugin's tier, signer and sandbox, and a revoked release", async () => {
    render(<Dialogs />);
    act(() => store.setUI({ dialog: { kind: "plugins" } }));
    expect(await screen.findByText("Automatic tab titles")).toBeInTheDocument();
    expect(screen.getByText("official")).toBeInTheDocument();
    expect(screen.getByText("SHA256:off1")).toBeInTheDocument();
    expect(screen.getByText("sandboxed (sandbox-exec)")).toBeInTheDocument();
    expect(screen.getByText("nobody (not signed)")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("Revoked: leaks the GitHub token");
    expect(screen.getByText("unlisted")).toBeInTheDocument();
    expect(screen.getByText("disabled")).toBeInTheDocument();
  });
});
