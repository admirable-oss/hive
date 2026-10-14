from typing import Any

from ..models.workspace import AgentList, Pane, Process, Tab, TerminalSnapshot, WorkspaceSnapshot
from ..protocol.connection import Connection


class Hive:
    """Typed wrappers for stable Hive daemon services plus generic call()."""

    def __init__(self, connection: Connection) -> None:
        self.connection = connection

    def call(self, method: str, params: Any = None) -> Any:
        return self.connection.call(method, params)

    def tabs(self, environment_id: str = "") -> list[Tab]:
        values = self.call("tab.list", {"environment_id": environment_id})
        return [Tab(**value) for value in values]

    def rename_tab(self, tab_id: str, name: str) -> Tab:
        return Tab(**self.call("tab.rename", {"id": tab_id, "name": name}))

    def popup(self, tab_id: str, command: list[str], *, name: str = "", cwd: str = "", env: dict[str, str] | None = None, width_pct: int = 80, height_pct: int = 80) -> Pane:
        spec: dict[str, Any] = {"command": command}
        if name: spec["name"] = name
        if cwd: spec["cwd"] = cwd
        if env: spec["env"] = env
        return Pane(**self.call("pane.popup", {"tab_id": tab_id, "spec": spec, "width_pct": width_pct, "height_pct": height_pct}))

    def processes(self, environment_id: str = "") -> list[Process]:
        return [Process(**value) for value in self.call("process.list", {"environment_id": environment_id})]

    def agents(self, all: bool = True) -> AgentList:
        return AgentList.from_dict(self.call("agent.list", {"all": all}))

    def workspace_snapshot(self) -> WorkspaceSnapshot:
        return WorkspaceSnapshot.from_dict(self.call("api.snapshot"))

    def terminal_snapshot(self, process_id: str) -> TerminalSnapshot:
        return TerminalSnapshot.from_dict(self.call("terminal.snapshot", {"process_id": process_id}))
