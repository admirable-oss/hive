// src/index.ts
import { join as join2 } from "node:path";

// ../../../sdk/typescript/dist/index.js
import { createConnection } from "node:net";
import { homedir } from "node:os";
import { join } from "node:path";
import { stdin } from "node:process";
function isPluginContext(value) {
  if (!value || typeof value !== "object") return false;
  const candidate = value;
  return typeof candidate.plugin === "string" && typeof candidate.trigger === "string" && typeof candidate.session === "string";
}
function mergeContributions(a, b) {
  const merged = {
    commands: [...a.commands ?? [], ...b.commands ?? []],
    views: [...a.views ?? [], ...b.views ?? []],
    keybindings: [...a.keybindings ?? [], ...b.keybindings ?? []]
  };
  for (const key of ["commands", "views", "keybindings"]) {
    const ids = (merged[key] ?? []).map((item) => "id" in item ? item.id : item.key);
    if (new Set(ids).size !== ids.length) throw new Error(`duplicate plugin ${key} contribution ID`);
  }
  return merged;
}
var HiveProtocolError = class extends Error {
  constructor(code, message, method) {
    super(message);
    this.code = code;
    this.method = method;
    this.name = "HiveProtocolError";
  }
};
var Protocol;
((Protocol2) => {
  Protocol2.MAX_MESSAGE_BYTES = 1 << 20;
})(Protocol || (Protocol = {}));
var HiveConnection = class _HiveConnection {
  constructor(socket, welcome) {
    this.socket = socket;
    this.welcome = welcome;
    socket.on("data", (chunk) => this.receive(typeof chunk === "string" ? chunk : new TextDecoder().decode(chunk)));
    socket.on("error", (error) => this.failAll(error));
    socket.on("close", () => this.failAll(new Error("Hive daemon connection closed")));
  }
  buffer = "";
  sequence = 0;
  pending = /* @__PURE__ */ new Map();
  closed = false;
  static async connect(options, connector = (path) => createConnection(path)) {
    const socketPath = options.socketPath ?? process.env.HIVE_SOCKET_PATH ?? join(homedir(), ".hive", "hive.sock");
    const socket = connector(socketPath);
    await new Promise((resolve, reject) => {
      const timer = options.timeoutMs ? setTimeout(() => reject(new Error(`Hive socket connection timed out after ${options.timeoutMs}ms`)), options.timeoutMs) : void 0;
      socket.once("connect", () => {
        if (timer) clearTimeout(timer);
        resolve();
      });
      socket.once("error", (error) => {
        if (timer) clearTimeout(timer);
        reject(error);
      });
    }).catch((error) => {
      socket.destroy();
      throw error;
    });
    const hello = {
      version: "2",
      type: "hello",
      id: "hello",
      params: { client: `hive-plugin:${options.id}`, client_version: options.version ?? "0.0.0", capabilities: options.capabilities ?? [] }
    };
    const welcome = await new Promise((resolve, reject) => {
      let data = "";
      const timer = options.timeoutMs ? setTimeout(() => reject(new Error("Hive protocol handshake timed out")), options.timeoutMs) : void 0;
      const cleanup = () => {
        if (timer) clearTimeout(timer);
        socket.off("data", onData);
        socket.off("error", onError);
      };
      const onError = (error) => {
        cleanup();
        reject(error);
      };
      const onData = (chunk) => {
        data += typeof chunk === "string" ? chunk : new TextDecoder().decode(chunk);
        const newline = data.indexOf("\n");
        if (newline < 0) return;
        cleanup();
        try {
          const message = JSON.parse(data.slice(0, newline));
          if (message.type !== "welcome" || !message.result) throw new Error(`expected protocol welcome, received ${message.type}`);
          if (message.result.protocol !== "2") throw new Error(`Hive negotiated unsupported protocol ${message.result.protocol}`);
          resolve(message.result);
        } catch (error) {
          reject(error);
        }
      };
      socket.on("data", onData);
      socket.once("error", onError);
      socket.write(`${JSON.stringify(hello)}
`);
    }).catch((error) => {
      socket.destroy();
      throw error;
    });
    return new _HiveConnection(socket, welcome);
  }
  call(method, params = {}) {
    if (this.closed) return Promise.reject(new Error("Hive daemon connection is closed"));
    const id = String(++this.sequence);
    return new Promise((resolve, reject) => {
      this.pending.set(id, { method, resolve: (value) => resolve(value), reject });
      const message = { version: "2", type: "request", id, method, params };
      const frame = `${JSON.stringify(message)}
`;
      if (new TextEncoder().encode(frame).byteLength > Protocol.MAX_MESSAGE_BYTES) {
        this.pending.delete(id);
        reject(new Error(`Hive protocol frame exceeds ${Protocol.MAX_MESSAGE_BYTES} bytes`));
        return;
      }
      this.socket.write(frame, (error) => {
        if (error) {
          this.pending.delete(id);
          reject(error);
        }
      });
    });
  }
  close() {
    if (!this.closed) {
      this.closed = true;
      this.socket.end();
      this.failAll(new Error("Hive daemon connection closed by plugin"));
    }
  }
  receive(chunk) {
    this.buffer += chunk;
    if (new TextEncoder().encode(this.buffer).byteLength > Protocol.MAX_MESSAGE_BYTES && !this.buffer.includes("\n")) {
      this.failAll(new Error(`Hive protocol frame exceeds ${Protocol.MAX_MESSAGE_BYTES} bytes`));
      this.socket.destroy();
      return;
    }
    for (; ; ) {
      const newline = this.buffer.indexOf("\n");
      if (newline < 0) break;
      const frame = this.buffer.slice(0, newline);
      this.buffer = this.buffer.slice(newline + 1);
      if (!frame) continue;
      let message;
      try {
        message = JSON.parse(frame);
      } catch (error) {
        this.failAll(new Error(`invalid Hive protocol frame: ${String(error)}`));
        this.socket.destroy();
        return;
      }
      if (message.type !== "response" || !message.id) continue;
      const pending = this.pending.get(message.id);
      if (!pending) continue;
      this.pending.delete(message.id);
      if (message.error) pending.reject(new HiveProtocolError(message.error.code, message.error.message, pending.method));
      else pending.resolve(message.result);
    }
  }
  failAll(error) {
    if (this.closed && this.pending.size === 0) return;
    this.closed = true;
    for (const item of this.pending.values()) item.reject(error);
    this.pending.clear();
  }
};
function createHiveAPI(connection) {
  const call = (method, params) => connection.call(method, params);
  return {
    call,
    tabs: {
      list: (environment_id = "") => call("tab.list", { environment_id }),
      rename: (id, name) => call("tab.rename", { id, name }),
      focus: (id) => call("tab.focus", { id })
    },
    panes: {
      list: (environment_id = "", tab_id = "") => call("pane.list", { environment_id, tab_id }),
      popup: (request) => call("pane.popup", request)
    },
    processes: { list: (environment_id = "") => call("process.list", { environment_id }) },
    agents: { list: (all = true) => call("agent.list", { all }) },
    workspace: { snapshot: () => call("api.snapshot") },
    terminal: { snapshot: (process_id) => call("terminal.snapshot", { process_id }) }
  };
}
async function init(options, connector) {
  const context = options.context ?? await readContext();
  if (context.plugin !== options.id) throw new Error(`plugin context ID ${context.plugin} does not match SDK ID ${options.id}`);
  const connection = await HiveConnection.connect(options, connector);
  const hive = createHiveAPI(connection);
  const handlers = /* @__PURE__ */ new Map();
  let contribution = {};
  const plugin2 = {
    context,
    hive,
    on(eventType, handler) {
      handlers.set(eventType, [...handlers.get(eventType) ?? [], handler]);
      return plugin2;
    },
    extend(value) {
      contribution = mergeContributions(contribution, value);
      return plugin2;
    },
    async run() {
      if (Object.keys(contribution).length && !connection.welcome.capabilities?.includes("plugin.contributions")) {
        throw new Error("Hive daemon does not advertise plugin.contributions; upgrade Hive to enable UI extensions");
      }
      if (Object.keys(contribution).length) await hive.call("plugin.contribute", { plugin: options.id, contribution });
      if (context.trigger === "event" && context.event) {
        const matching = [...handlers.get(context.event.type) ?? [], ...handlers.get("*") ?? []];
        for (const handler of matching) await handler(context.event, context);
      }
    },
    close: () => connection.close()
  };
  return plugin2;
}
async function readContext(input = stdin) {
  let raw = "";
  for await (const chunk of input) raw += typeof chunk === "string" ? chunk : new TextDecoder().decode(chunk);
  if (new TextEncoder().encode(raw).byteLength > 1 << 20) throw new Error("Hive plugin context exceeds 1 MiB");
  let value;
  try {
    value = JSON.parse(raw);
  } catch (error) {
    throw new Error(`invalid Hive plugin context JSON: ${String(error)}`);
  }
  if (!isPluginContext(value)) throw new Error("invalid Hive plugin context: plugin, trigger and session are required");
  return value;
}

// src/index.ts
var plugin = await init({ id: "hive-explorer", version: "0.1.0" });
try {
  if (plugin.context.trigger === "action") {
    const dir = process.env.HIVE_PLUGIN_DIR;
    if (!dir) throw new Error("Hive did not provide HIVE_PLUGIN_DIR");
    if (!plugin.context.tab) throw new Error("Hive Explorer needs an active tab");
    await plugin.hive.panes.popup({
      tab_id: plugin.context.tab,
      spec: {
        name: "Hive Explorer",
        command: [process.execPath, join2(dir, "dist/ui.mjs")],
        cwd: process.cwd(),
        env: { HIVE_PLUGIN_DIR: dir }
      },
      width_pct: 92,
      height_pct: 88
    });
  }
} finally {
  plugin.close();
}
