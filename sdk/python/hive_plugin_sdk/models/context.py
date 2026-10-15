from dataclasses import dataclass, field
from typing import Any


@dataclass(frozen=True, slots=True)
class Event:
    type: str
    data: Any = None
    # The daemon's event number: events reach a plugin in seq order.
    seq: int = 0
    time: str | None = None
    # Events were lost before this one: re-read the state you follow.
    missed: bool = False


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
    # The plugin API version Hive runs the plugin under (0 before API 1).
    api: int = 0
    # This action run's ID (plugin.cancel takes it).
    run: str | None = None
    # The capabilities the plugin holds now.
    capabilities: tuple[str, ...] = field(default_factory=tuple)

    def has(self, capability: str) -> bool:
        return capability in self.capabilities

    @classmethod
    def from_dict(cls, value: dict[str, Any]) -> "PluginContext":
        event = value.get("event")
        result = cls(
            plugin=value.get("plugin", ""),
            trigger=value.get("trigger", ""),
            session=value.get("session", ""),
            action=value.get("action"),
            event=Event(
                type=event.get("type", ""), data=event.get("data"), seq=int(event.get("seq", 0)),
                time=event.get("time"), missed=bool(event.get("missed", False)),
            ) if event else None,
            environment=value.get("environment"),
            tab=value.get("tab"),
            pane=value.get("pane"),
            api=int(value.get("api", 0)),
            run=value.get("run"),
            capabilities=tuple(value.get("capabilities") or ()),
        )
        if not result.plugin or not result.trigger or not result.session:
            raise ValueError("plugin, trigger, and session are required in Hive invocation context")
        return result
