import { EventEmitter } from "node:events";
import { readFile } from "node:fs/promises";
import { describe, expect, test } from "bun:test";
import { HiveConnection, HiveProtocolError, init, isPluginContext, readContext, type Protocol, type ProtocolTransport } from "../src/index.js";

class FakeSocket extends EventEmitter {
  private buffer = "";
  private deliveries = Promise.resolve();
  setEncoding(_encoding: string): this { return this; }
  write(chunk: string, callback?: (error?: Error | null) => void): boolean {
    this.buffer += chunk;
    for (;;) {
      const end = this.buffer.indexOf("\n"); if (end < 0) break;
      const line = this.buffer.slice(0, end); this.buffer = this.buffer.slice(end + 1);
      const request = JSON.parse(line) as Protocol.Envelope;
      let response: Protocol.Envelope;
      if (request.type === "hello") {
        response = { version: "2", type: "welcome", id: "hello", result: { protocol: "2", server_version: "test", capabilities: [] } };
      } else {
        response = request.method === "test.fail"
          ? { version: "2", type: "response", id: request.id, error: { code: "not_found", message: "missing test object" } }
          : {
              version: "2", type: "response", id: request.id,
              result: request.method === "tab.rename" ? { id: (request.params as { id: string }).id, name: (request.params as { name: string }).name } : { tabs: [], panes: [], processes: [], agents: [] },
            };
      }
      const frame = `${JSON.stringify(response)}\n`;
      // Split every frame to verify the SDK retains partial lines between reads.
      this.deliveries = this.deliveries.then(() => new Promise<void>(resolve => {
        this.emit("data", Buffer.from(frame.slice(0, 9)));
        setTimeout(() => { this.emit("data", Buffer.from(frame.slice(9))); resolve(); }, 1);
      }));
    }
    callback?.(null);
    return true;
  }
  end(): this { queueMicrotask(() => this.emit("close")); return this; }
  destroy(): this { queueMicrotask(() => this.emit("close")); return this; }
}

function fakeConnector() {
  const socket = new FakeSocket();
  queueMicrotask(() => socket.emit("connect"));
  return socket as unknown as ProtocolTransport;
}

describe("Hive protocol 2 TypeScript SDK", () => {
  test("shared golden fixtures decode as protocol envelopes", async () => {
    const fixture = (name: string) => readFile(new URL(`../../protocol/fixtures/${name}`, import.meta.url), "utf8");
    const hello = JSON.parse(await fixture("hello.json")) as Protocol.Envelope<Protocol.Hello>;
    const welcome = JSON.parse(await fixture("welcome.json")) as Protocol.Envelope<Protocol.Welcome>;
    const request = JSON.parse(await fixture("request-tab-rename.json")) as Protocol.Envelope;
    expect(hello).toMatchObject({ type: "hello", version: "2", params: { client: "hive-plugin:example" } });
    expect(welcome.result?.protocol).toBe("2");
    expect(request).toMatchObject({ type: "request", method: "tab.rename", params: { id: "tab-1", name: "Build" } });
  });

  test("parses and validates plugin invocation context", async () => {
    const context = await readContext((async function* () { yield "{\"plugin\":\"demo\","; yield "\"trigger\":\"event\",\"session\":\"s1\"}"; })());
    expect(context).toEqual({ plugin: "demo", trigger: "event", session: "s1" });
    expect(isPluginContext({ plugin: "demo", trigger: "startup", session: "s" })).toBe(true);
    expect(isPluginContext({ plugin: "demo", trigger: "startup" })).toBe(false);
    await expect(readContext((async function* () { yield " ".repeat((1 << 20) + 1); })())).rejects.toThrow("exceeds 1 MiB");
  });

  test("negotiates hello/welcome, correlates calls and handles split JSON lines", async () => {
    const connection = await HiveConnection.connect({ id: "test-plugin", socketPath: "/fake/hive.sock" }, fakeConnector);
    expect(connection.welcome).toMatchObject({ protocol: "2", server_version: "test" });
    const tabs = await Promise.all([
      connection.call<{ id: string; name: string }>("tab.rename", { id: "t1", name: "Build" }),
      connection.call<{ id: string; name: string }>("tab.rename", { id: "t2", name: "Review" }),
    ]);
    expect(tabs).toEqual([{ id: "t1", name: "Build" }, { id: "t2", name: "Review" }]);
    await expect(connection.call("test.fail")).rejects.toBeInstanceOf(HiveProtocolError);
    await expect(connection.call("test.fail")).rejects.toMatchObject({ code: "not_found", message: "missing test object" });
    connection.close();
  });

  test("init exposes typed Hive extension methods over the negotiated connection", async () => {
    const plugin = await init({
      id: "test-plugin", socketPath: "/fake/hive.sock",
      context: { plugin: "test-plugin", trigger: "action", session: "s1" },
    }, fakeConnector);
    expect(plugin.context.trigger).toBe("action");
    expect(await plugin.hive.tabs.rename("t1", "Explorer")).toEqual({ id: "t1", name: "Explorer" });
    plugin.close();
  });

  test("dispatches only the matching invocation event to registered handlers", async () => {
    const plugin = await init({
      id: "test-plugin", socketPath: "/fake/hive.sock",
      context: { plugin: "test-plugin", trigger: "event", session: "s1", event: { type: "process.output", data: { id: "p1" } } },
    }, fakeConnector);
    const observed: string[] = [];
    plugin.on<{ id: string }>("process.output", event => { observed.push(event.data?.id ?? "missing"); });
    plugin.on("agent.state", () => { observed.push("wrong-handler"); });
    await plugin.run();
    expect(observed).toEqual(["p1"]);
    plugin.close();
  });
});
