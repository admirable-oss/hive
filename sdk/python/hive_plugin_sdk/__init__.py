"""Official Python SDK for Hive plugin processes and protocol 2."""

from .models import Agent, AgentList, Event, Pane, PluginContext, Process, Tab, TerminalSnapshot, WorkspaceSnapshot
from .models.errors import HiveProtocolError
from .plugin import Plugin, init
from .protocol import Connection, Envelope, Hello, Welcome
from .services import Hive
from .views import Card, CardRow, ExtensionContribution, Link, Status

__all__ = [
    "Agent", "AgentList", "Card", "CardRow", "Connection", "Envelope", "Event", "ExtensionContribution", "Hello", "Link", "Status",
    "Hive", "HiveProtocolError", "Pane", "Plugin", "PluginContext", "Process",
    "Tab", "TerminalSnapshot", "Welcome", "WorkspaceSnapshot", "init",
]
