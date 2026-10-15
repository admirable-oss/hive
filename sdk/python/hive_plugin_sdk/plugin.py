from __future__ import annotations

import json
import sys
from collections import defaultdict
from typing import Any, Callable, TextIO

from .models.context import Event, PluginContext
from .protocol.connection import Connection
from .services.hive import Hive
from .views import Card, ExtensionContribution, Status

EventHandler = Callable[[Event, PluginContext], Any]


class Plugin:
    def __init__(self, context: PluginContext, connection: Connection) -> None:
        self.context = context
        self.hive = Hive(connection)
        self._handlers: dict[str, list[EventHandler]] = defaultdict(list)

    def on(self, event_type: str, handler: EventHandler) -> "Plugin":
        self._handlers[event_type].append(handler)
        return self

    def has(self, capability: str) -> bool:
        """Whether the plugin holds a capability now."""
        return self.context.has(capability)

    def set_card(self, card: Card) -> None:
        """Show (or clear) one of the cards the manifest declares."""
        self.hive.call("plugin.card", card.params())

    def set_status(self, status: Status) -> None:
        """Show (or clear) one of the status indicators the manifest declares."""
        self.hive.call("plugin.status", status.params())

    def ready(self) -> None:
        """Say startup is done (otherwise 2s of uptime counts)."""
        self.hive.call("plugin.ready", {})

    def request_capability(self, capability: str, reason: str = "") -> None:
        """Ask the user for an optional capability; the user grants it."""
        self.hive.call("plugin.request", {"capability": capability, "reason": reason})

    def capabilities(self) -> dict[str, Any]:
        """What this hive offers, and what this plugin may call."""
        return self.hive.call("plugin.capabilities", {})

    def extend(self, contribution: ExtensionContribution) -> "Plugin":
        raise RuntimeError("UI contributions are declared in hive-plugin.toml [contributes] since plugin API 1; "
                           "set cards and indicators with set_card/set_status")

    def run(self) -> None:
        if self.context.trigger == "event" and self.context.event:
            for handler in self._handlers[self.context.event.type] + self._handlers["*"]:
                handler(self.context.event, self.context)

    def close(self) -> None:
        self.hive.connection.close()


def init(*, context: PluginContext | None = None, stream: TextIO | None = None, socket_path: str | None = None, version: str = "0.1.0") -> Plugin:
    """Read Hive's stdin context and connect, or initialize explicit context."""
    if context is None:
        source = stream or sys.stdin
        raw = source.read((1 << 20) + 1)
        if len(raw.encode()) > 1 << 20:
            raise ValueError("Hive plugin context exceeds 1 MiB")
        value = json.loads(raw)
        if not isinstance(value, dict):
            raise ValueError("Hive plugin context must be a JSON object")
        context = PluginContext.from_dict(value)
    connection = Connection.connect(context.plugin, version, socket_path)
    return Plugin(context, connection)
