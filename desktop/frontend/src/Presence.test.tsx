import { render, screen } from "@testing-library/react";

vi.mock("./api", () => ({
  api: new Proxy({}, { get: () => () => Promise.resolve("") }),
  listen: () => () => {},
  isMac: () => false,
}));

const { Others } = await import("./components/PaneGrid");

const pane = { id: "p1", tab_id: "t1", environment_id: "web", process_id: "x1", running: true };

describe("others at a pane", () => {
  it("names who is typing before who is looking", () => {
    render(<Others pane={{ ...pane, typing: "alice", watchers: ["bob"] }} />);
    expect(screen.getByRole("status")).toHaveTextContent("✎ alice");
    expect(screen.getByRole("status")).toHaveAccessibleName(/alice is typing here/);
  });

  it("lists who is looking", () => {
    render(<Others pane={{ ...pane, watchers: ["bob", "carol"] }} />);
    expect(screen.getByRole("status")).toHaveTextContent("bob, carol");
  });

  it("shows nothing when nobody else is there", () => {
    const { container } = render(<Others pane={pane} />);
    expect(container).toBeEmptyDOMElement();
  });
});
