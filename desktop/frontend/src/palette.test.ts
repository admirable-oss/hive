import { match } from "./components/Palette";

describe("match", () => {
  it("matches letters in order, adjacent ones best", () => {
    expect(match("clau", "claude · api")).not.toBeNull();
    expect(match("xyz", "claude")).toBeNull();
    expect(match("", "anything")).toBe(0);
    expect(match("api", "api reviewer")!).toBeLessThan(match("api", "a p i")!);
  });
});
