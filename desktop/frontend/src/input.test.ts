import { describe, expect, it } from "vitest";
import { queueInput } from "./input";

describe("queueInput", () => {
  it("delivers a pane's input in the order it was typed, however the calls finish", async () => {
    const got: string[] = [];
    let n = 0;
    // Calls finish in a scrambled order, as the bridge may run them.
    const send = (pane: string, data: string) =>
      new Promise<void>((resolve) => setTimeout(() => {
        got.push(`${pane}:${data}`);
        resolve();
      }, [7, 1, 4, 0, 3][n++ % 5]));
    const typed = "printf 'iVBORw0KGgo'";
    const calls = [...typed].map((c) => queueInput("p1", c, send));
    await Promise.all(calls);
    await new Promise((r) => setTimeout(r, 30));
    expect(got.map((g) => g.slice(3)).join("")).toBe(typed);
    expect(got.length).toBeLessThan(typed.length); // what was typed meanwhile went together
  });

  it("keeps panes apart, and drops what follows a failed call", async () => {
    const got: string[] = [];
    let fail = true;
    const send = async (pane: string, data: string) => {
      if (pane === "p2" && fail) {
        fail = false;
        throw new Error("gone");
      }
      got.push(`${pane}:${data}`);
    };
    await expect(queueInput("p2", "a", send)).rejects.toThrow("gone");
    await queueInput("p2", "b", send);
    await queueInput("p3", "c", send);
    expect(got).toEqual(["p2:b", "p3:c"]);
  });
});
