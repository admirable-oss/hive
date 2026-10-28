import { readFile } from "node:fs/promises";
import { describe, expect, test } from "bun:test";
import { Capability, readContext } from "../src/index.js";

const fixture = (name: string) => readFile(new URL(`../../protocol/fixtures/v2/${name}`, import.meta.url), "utf8");

describe("plugin API 2", () => {
  test("contexts carry API 2 and its capabilities", async () => {
    const raw = await fixture("context-action.json");
    const context = await readContext((async function* () { yield raw; })());
    expect(context.api).toBe(2);
    expect(context.capabilities).toContain(Capability.FilesystemReadWorkspace);
    expect(context.capabilities).toContain(Capability.Unconfined);
    const discovery = JSON.parse(await fixture("capabilities.json"));
    expect(discovery.api).toBe(2);
    const names = discovery.capabilities.map((c: { name: string }) => c.name);
    for (const c of Object.values(Capability)) expect(names).toContain(c);
  });
});
