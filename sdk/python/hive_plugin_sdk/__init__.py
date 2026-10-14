"""Official Python SDK for Hive plugin processes and protocol 2."""

from .models import Agent, AgentList, Event, Pane, PluginContext, Process, Tab, TerminalSnapshot, WorkspaceSnapshot
from .models.errors import HiveProtocolError
from .plugin import Plugin, init
from .protocol import Connection, Envelope, Hello, Welcome
from .services import Hive
from .views import ExtensionContribution

__all__ = [
    "Agent", "AgentList", "Connection", "Envelope", "Event", "ExtensionContribution", "Hello",
    "Hive", "HiveProtocolError", "Pane", "Plugin", "PluginContext", "Process",
    "Tab", "TerminalSnapshot", "Welcome", "WorkspaceSnapshot", "init",
]
