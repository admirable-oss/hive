from __future__ import annotations

import json
import os
import socket
import threading
from pathlib import Path
from typing import Any

from ..models.errors import HiveProtocolError
from .message import Welcome

MAX_MESSAGE_BYTES = 1 << 20


class Connection:
    """Synchronous protocol-2 JSON-lines client; calls are thread-safe."""

    def __init__(self, sock: socket.socket, plugin_id: str, version: str = "0.1.0") -> None:
        self._socket = sock
        self._reader = sock.makefile("rb")
        self._lock = threading.RLock()
        self._sequence = 0
        self._closed = False
        self.welcome = self._handshake(plugin_id, version)

    @classmethod
    def connect(cls, plugin_id: str, version: str = "0.1.0", socket_path: str | None = None) -> "Connection":
        path = socket_path or os.environ.get("HIVE_SOCKET_PATH") or str(Path.home() / ".hive" / "hive.sock")
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        sock.connect(path)
        try:
            return cls(sock, plugin_id, version)
        except BaseException:
            sock.close()
            raise

    def _handshake(self, plugin_id: str, version: str) -> Welcome:
        self._send({
            "version": "2", "type": "hello", "id": "hello",
            "params": {"client": f"hive-plugin:{plugin_id}", "client_version": version, "capabilities": []},
        })
        message = self._read()
        if message.get("type") != "welcome":
            raise RuntimeError(f"expected protocol welcome, received {message.get('type')}")
        result = message.get("result") or {}
        if result.get("protocol") != "2":
            raise RuntimeError(f"Hive negotiated unsupported protocol {result.get('protocol')}")
        return Welcome(**{key: result[key] for key in ("protocol", "server_version", "methods", "capabilities") if key in result})

    def call(self, method: str, params: Any = None) -> Any:
        with self._lock:
            if self._closed:
                raise RuntimeError("Hive daemon connection is closed")
            self._sequence += 1
            request_id = str(self._sequence)
            self._send({"version": "2", "type": "request", "id": request_id, "method": method, "params": {} if params is None else params})
            while True:
                response = self._read()
                if response.get("type") != "response" or response.get("id") != request_id:
                    continue
                if response.get("error"):
                    error = response["error"]
                    raise HiveProtocolError(error.get("code", "unknown"), error.get("message", "Hive request failed"), method)
                return response.get("result")

    def close(self) -> None:
        with self._lock:
            if self._closed:
                return
            self._closed = True
            self._reader.close()
            self._socket.close()

    def _send(self, value: dict[str, Any]) -> None:
        frame = json.dumps(value, separators=(",", ":")).encode() + b"\n"
        if len(frame) > MAX_MESSAGE_BYTES:
            raise ValueError(f"Hive protocol frame exceeds {MAX_MESSAGE_BYTES} bytes")
        self._socket.sendall(frame)

    def _read(self) -> dict[str, Any]:
        frame = self._reader.readline(MAX_MESSAGE_BYTES + 1)
        if not frame:
            raise ConnectionError("Hive daemon closed the connection")
        if len(frame) > MAX_MESSAGE_BYTES or not frame.endswith(b"\n"):
            raise ValueError(f"Hive protocol frame exceeds {MAX_MESSAGE_BYTES} bytes or is unterminated")
        return json.loads(frame)
