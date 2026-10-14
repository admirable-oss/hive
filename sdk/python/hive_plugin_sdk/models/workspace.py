from dataclasses import dataclass, field
from typing import Any


@dataclass(slots=True)
class Tab:
    id: str
    name: str
    environment_id: str | None = None


@dataclass(slots=True)
class Pane:
    id: str
    tab_id: str
    process_id: str | None = None


@dataclass(slots=True)
class Process:
    id: str
    command: str = ""
    args: list[str] = field(default_factory=list)
    status: str | None = None


@dataclass(slots=True)
class Agent:
    id: str
    name: str | None = None
    pane_id: str | None = None
    tab_id: str | None = None


@dataclass(slots=True)
class AgentList:
    agents: list[Agent]
    rollups: dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, value: dict[str, Any]) -> "AgentList":
        return cls(agents=[Agent(**item) for item in value.get("agents", [])], rollups=value.get("rollups", {}))


@dataclass(slots=True)
class WorkspaceSnapshot:
    tabs: list[Tab]
    panes: list[Pane]
    processes: list[Process]
    agents: list[Agent]
    daemon: dict[str, Any] | None = None
    environments: list[dict[str, Any]] = field(default_factory=list)
    rollups: dict[str, Any] | None = None

    @classmethod
    def from_dict(cls, value: dict[str, Any]) -> "WorkspaceSnapshot":
        return cls(
            tabs=[Tab(**item) for item in value.get("tabs", [])],
            panes=[Pane(**item) for item in value.get("panes", [])],
            processes=[Process(**item) for item in value.get("processes", [])],
            agents=[Agent(**item) for item in value.get("agents", [])],
            daemon=value.get("daemon"), environments=value.get("environments", []), rollups=value.get("rollups"),
        )


@dataclass(slots=True)
class TerminalSnapshot:
    title: str | None = None
    lines: list[str] = field(default_factory=list)
    width: int = 0
    height: int = 0

    @classmethod
    def from_dict(cls, value: dict[str, Any]) -> "TerminalSnapshot":
        return cls(title=value.get("title"), lines=value.get("lines", []), width=value.get("width", 0), height=value.get("height", 0))
