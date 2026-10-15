import { EventEmitter } from "node:events";
import { readFile } from "node:fs/promises";
import { describe, expect, test } from "bun:test";
import { Capability, init, readContext, ttlNanos, type Protocol, type ProtocolTransport } from "../src/index.js";

const fixture = (name: string) => readFile(new URL(`../../protocol/fixtures/v1/${name}`, import.meta.url), "utf8");

/** A daemon that answers every call with {} and remembers the requests. */
function recordingConnector(requests: Protocol.Envelope[]) {
  return () => {
    const socket = new EventEmitter() as EventEmitter & Record<string, unknown>;
    let buffer = "";
    socket.setEncoding = () => socket;
    socket.write = (chunk: string, callback?: (error?: Error | null) => void) => {
      buffer += chunk;
      for (let end = buffer.indexOf("\n"); end >= 0; end = buffer.indexOf("\n")) {
        const request = JSON.parse(buffer.slice(0, end)) as Protocol.Envelope;
        buffer = buffer.slice(end + 1);
        const response: Protocol.Envelope = request.type === "hello"
          ? { version: "2", type: "welcome", id: "hello", result: { protocol: "2", server_version: "test", capabilities: [] } }
          : { version: "2", type: "response", id: request.id, result: {} };
        if (request.type !== "hello") requests.push(request);
        queueMicrotask(() => socket.emit("data", Buffer.from(`${JSON.stringify(response)}\n`)));
      }
      callback?.(null);
      return true;
    };
    socket.end = () => { queueMicrotask(() => socket.emit("close")); return socket; };
    socket.destroy = socket.end;
    queueMicrotask(() => socket.emit("connect"));
    return socket as unknown as ProtocolTransport;
  };
}

describe("plugin API 1", () => {
  test("contexts from the shared fixtures carry API 1 fields", async () => {
    for (const name of ["context-action.json", "context-event.json"]) {
      const raw = await fixture(name);
      const context = await readContext((async function* () { yield raw; })());
      expect(context.api).toBe(1);
      expect(context.capabilities).toEqual([Capability.HiveRead, Capability.HiveWrite]);
      if (context.trigger === "event") {
        expect(context.event).toMatchObject({ type: "worktree.created", seq: 42 });
      } else {
        expect(context).toMatchObject({ action: "show", environment: "api", pane: "p1" });
      }
    }
    const discovery = JSON.parse(await fixture("capabilities.json"));
    expect(discovery.api).toBe(1);
    expect(discovery.methods).toContain("plugin.card");
  });

  test("setCard and setStatus send the wire shape, with TTLs in nanoseconds", async () => {
    const requests: Protocol.Envelope[] = [];
    const context = JSON.parse(await fixture("context-action.json"));
    const plugin = await init({ id: "contract", socketPath: "/unused", context }, recordingConnector(requests));
    expect(plugin.has(Capability.HiveWrite)).toBe(true);
    expect(plugin.has(Capability.Network)).toBe(false);
    await plugin.setCard({ card: "build", environment: "api", rows: [{ label: "main", text: "passing", tone: "success" }], links: [{ title: "Run", url: "https://ci.example.com/run/1" }], ttlSeconds: 600 });
    await plugin.setStatus({ indicator: "builds", text: "2 green" });
    await plugin.ready();
    const want = JSON.parse(await fixture("request-plugin-card.json"));
    expect(requests[0]).toMatchObject({ type: "request", method: "plugin.card", params: want.params });
    expect(requests[1]).toMatchObject({ method: "plugin.status", params: { indicator: "builds", text: "2 green" } });
    expect(requests[2].method).toBe("plugin.ready");
    expect(() => plugin.extend({ commands: [] })).toThrow("hive-plugin.toml [contributes]");
    expect(ttlNanos(1.5)).toBe(1_500_000_000);
    plugin.close();
  });
});
