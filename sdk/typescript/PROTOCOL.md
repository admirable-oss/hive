# Hive plugin protocol contract

The TypeScript package connects to `HIVE_SOCKET_PATH`, the Unix socket exposed
to the plugin process. It opens one connection per invocation and speaks Hive
protocol 2 using UTF-8 JSON Lines. Each frame is one JSON object followed by a
newline; current daemon maximum frame size is 1 MiB.

The first client frame is a `hello` envelope with `version: "2"`, a client
identifier, SDK version, and requested capabilities. The server replies with a
`welcome` whose result reports protocol, server version, method names, and
capabilities. A plugin call is a `request` with a unique string ID, method, and
JSON params. The matching `response` carries either a result or a structured
error with a stable code and message. The client buffers partial frames and
maps concurrent responses back to their request promises.

The envelope also defines `stream`, `data`, and `close` for protocol pipe
traffic. The initial public SDK uses typed request/response methods; raw pipe
stream helpers remain future work. Discover daemon methods with `api.schema`
instead of guessing endpoint names. See Hive's `internal/protocol` package for
the canonical implementation and `api.schema` for the live service catalog.
