/** Hive's protocol 2 JSON-lines wire types. */
export namespace Protocol {
  export const MAX_MESSAGE_BYTES = 1 << 20;
  export type MessageType = "hello" | "welcome" | "request" | "response" | "data" | "close";
  export interface Error { code: string; message: string; }
  export interface Envelope<T = unknown> {
    version?: string;
    type: MessageType;
    id?: string;
    method?: string;
    params?: unknown;
    result?: T;
    error?: Error;
    stream?: string;
    data?: string;
  }
  export interface Hello { client: string; client_version?: string; capabilities?: string[]; }
  export interface Welcome { protocol: string; server_version?: string; methods?: string[]; capabilities?: string[]; }
}

/** Structural transport interface keeps @types/node out of consumer declarations. */
export interface ProtocolTransport {
  on(event: "data", listener: (chunk: string | Uint8Array) => void): unknown;
  on(event: "error", listener: (error: Error) => void): unknown;
  on(event: "close", listener: () => void): unknown;
  once(event: "connect", listener: () => void): unknown;
  once(event: "error", listener: (error: Error) => void): unknown;
  off(event: "data", listener: (chunk: string | Uint8Array) => void): unknown;
  off(event: "error", listener: (error: Error) => void): unknown;
  write(data: string, callback?: (error?: Error | null) => void): boolean;
  end(): unknown;
  destroy(): unknown;
}
