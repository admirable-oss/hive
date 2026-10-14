from dataclasses import dataclass
from typing import Any


@dataclass(frozen=True, slots=True)
class Event:
    type: str
    data: Any = None


@dataclass(frozen=True, slots=True)
class PluginContext:
    plugin: str
    trigger: str
    session: str
    action: str | None = None
    event: Event | None = None
    environment: str | None = None
    tab: str | None = None
    pane: str | None = None

    @classmethod
    def from_dict(cls, value: dict[str, Any]) -> "PluginContext":
        event = value.get("event")
        result = cls(
            plugin=value.get("plugin", ""),
            trigger=value.get("trigger", ""),
            session=value.get("session", ""),
            action=value.get("action"),
            event=Event(type=event.get("type", ""), data=event.get("data")) if event else None,
            environment=value.get("environment"),
            tab=value.get("tab"),
            pane=value.get("pane"),
        )
        if not result.plugin or not result.trigger or not result.session:
            raise ValueError("plugin, trigger, and session are required in Hive invocation context")
        return result
