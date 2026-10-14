import { createConnection } from "node:net";
import { homedir } from "node:os";
import { join } from "node:path";
import type { InitOptions } from "../services/options.js";
import { HiveProtocolError } from "../models/errors.js";
import { Protocol, type ProtocolTransport } from "./types.js";

/** Multiplexed protocol-2 JSON-lines client with correlated concurrent calls. */
export class HiveConnection {
  private buffer = "";
  private sequence = 0;
  private readonly pending = new Map<string, { method: string; resolve(value: unknown): void; reject(error: Error): void }>();
  private closed = false;

  private constructor(private readonly socket: ProtocolTransport, readonly welcome: Protocol.Welcome) {
    socket.on("data", chunk => this.receive(typeof chunk === "string" ? chunk : new TextDecoder().decode(chunk)));
    socket.on("error", error => this.failAll(error));
    socket.on("close", () => this.failAll(new Error("Hive daemon connection closed")));
  }

  static async connect(
    options: InitOptions,
    connector: (socketPath: string) => ProtocolTransport = path => createConnection(path),
  ): Promise<HiveConnection> {
    const socketPath = options.socketPath ?? process.env.HIVE_SOCKET_PATH ?? join(homedir(), ".hive", "hive.sock");
    const socket = connector(socketPath);
    await new Promise<void>((resolve, reject) => {
      const timer = options.timeoutMs ? setTimeout(() => reject(new Error(`Hive socket connection timed out after ${options.timeoutMs}ms`)), options.timeoutMs) : undefined;
      socket.once("connect", () => { if (timer) clearTimeout(timer); resolve(); });
      socket.once("error", error => { if (timer) clearTimeout(timer); reject(error); });
    }).catch(error => { socket.destroy(); throw error; });

    const hello: Protocol.Envelope = {
      version: "2", type: "hello", id: "hello",
      params: { client: `hive-plugin:${options.id}`, client_version: options.version ?? "0.0.0", capabilities: options.capabilities ?? [] } satisfies Protocol.Hello,
    };
    const welcome = await new Promise<Protocol.Welcome>((resolve, reject) => {
      let data = "";
      const timer = options.timeoutMs ? setTimeout(() => reject(new Error("Hive protocol handshake timed out")), options.timeoutMs) : undefined;
      const cleanup = () => { if (timer) clearTimeout(timer); socket.off("data", onData); socket.off("error", onError); };
      const onError = (error: Error) => { cleanup(); reject(error); };
      const onData = (chunk: string | Uint8Array) => {
        data += typeof chunk === "string" ? chunk : new TextDecoder().decode(chunk);
        const newline = data.indexOf("\n");
        if (newline < 0) return;
        cleanup();
        try {
          const message = JSON.parse(data.slice(0, newline)) as Protocol.Envelope<Protocol.Welcome>;
          if (message.type !== "welcome" || !message.result) throw new Error(`expected protocol welcome, received ${message.type}`);
          if (message.result.protocol !== "2") throw new Error(`Hive negotiated unsupported protocol ${message.result.protocol}`);
          resolve(message.result);
        } catch (error) { reject(error); }
      };
      socket.on("data", onData);
      socket.once("error", onError);
      socket.write(`${JSON.stringify(hello)}\n`);
    }).catch(error => { socket.destroy(); throw error; });
    return new HiveConnection(socket, welcome);
  }

  call<T>(method: string, params: unknown = {}): Promise<T> {
    if (this.closed) return Promise.reject(new Error("Hive daemon connection is closed"));
    const id = String(++this.sequence);
    return new Promise<T>((resolve, reject) => {
      this.pending.set(id, { method, resolve: value => resolve(value as T), reject });
      const message: Protocol.Envelope = { version: "2", type: "request", id, method, params };
      const frame = `${JSON.stringify(message)}\n`;
      if (new TextEncoder().encode(frame).byteLength > Protocol.MAX_MESSAGE_BYTES) {
        this.pending.delete(id);
        reject(new Error(`Hive protocol frame exceeds ${Protocol.MAX_MESSAGE_BYTES} bytes`));
        return;
      }
      this.socket.write(frame, error => {
        if (error) { this.pending.delete(id); reject(error); }
      });
    });
  }

  close(): void {
    if (!this.closed) {
      this.closed = true;
      this.socket.end();
      this.failAll(new Error("Hive daemon connection closed by plugin"));
    }
  }

  private receive(chunk: string): void {
    this.buffer += chunk;
    if (new TextEncoder().encode(this.buffer).byteLength > Protocol.MAX_MESSAGE_BYTES && !this.buffer.includes("\n")) {
      this.failAll(new Error(`Hive protocol frame exceeds ${Protocol.MAX_MESSAGE_BYTES} bytes`));
      this.socket.destroy();
      return;
    }
    for (;;) {
      const newline = this.buffer.indexOf("\n");
      if (newline < 0) break;
      const frame = this.buffer.slice(0, newline);
      this.buffer = this.buffer.slice(newline + 1);
      if (!frame) continue;
      let message: Protocol.Envelope;
      try { message = JSON.parse(frame) as Protocol.Envelope; }
      catch (error) { this.failAll(new Error(`invalid Hive protocol frame: ${String(error)}`)); this.socket.destroy(); return; }
      if (message.type !== "response" || !message.id) continue;
      const pending = this.pending.get(message.id);
      if (!pending) continue;
      this.pending.delete(message.id);
      if (message.error) pending.reject(new HiveProtocolError(message.error.code, message.error.message, pending.method));
      else pending.resolve(message.result);
    }
  }

  private failAll(error: Error): void {
    if (this.closed && this.pending.size === 0) return;
    this.closed = true;
    for (const item of this.pending.values()) item.reject(error);
    this.pending.clear();
  }
}
