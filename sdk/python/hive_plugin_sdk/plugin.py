from __future__ import annotations

import json
import sys
from collections import defaultdict
from typing import Any, Callable, TextIO

from .models.context import Event, PluginContext
from .protocol.connection import Connection
from .services.hive import Hive
from .views import ExtensionContribution

EventHandler = Callable[[Event, PluginContext], Any]


class Plugin:
    def __init__(self, context: PluginContext, connection: Connection) -> None:
        self.context = context
        self.hive = Hive(connection)
        self._handlers: dict[str, list[EventHandler]] = defaultdict(list)
        self._contribution = ExtensionContribution()

    def on(self, event_type: str, handler: EventHandler) -> "Plugin":
        self._handlers[event_type].append(handler)
        return self

    def extend(self, contribution: ExtensionContribution) -> "Plugin":
        self._contribution = self._contribution.merge(contribution)
        return self

    def run(self) -> None:
        connection = self.hive.connection
        contribution = self._contribution
        if contribution.commands or contribution.views or contribution.keybindings:
            if "plugin.contributions" not in connection.welcome.capabilities:
                raise RuntimeError("Hive daemon does not advertise plugin.contributions; upgrade Hive to enable UI extensions")
            self.hive.call("plugin.contribute", {"plugin": self.context.plugin, "contribution": {
                "commands": contribution.commands, "views": contribution.views, "keybindings": contribution.keybindings,
            }})
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
