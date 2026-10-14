export class HiveProtocolError extends Error {
  constructor(readonly code: string, message: string, readonly method?: string) {
    super(message);
    this.name = "HiveProtocolError";
  }
}
