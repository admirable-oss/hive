"""Plugin API 1 UI surfaces.

What a plugin adds to Hive's UI is declared in hive-plugin.toml under
[contributes]; Hive draws it from data. At run time a plugin sets the
content of its declared cards and status indicators (Plugin.set_card,
Plugin.set_status).
"""

from dataclasses import asdict, dataclass, field
from typing import Any

# Capabilities a plugin may hold.
HIVE_READ = "hive.read"
HIVE_WRITE = "hive.write"
HIVE_INPUT = "hive.input"
HIVE_PROCESS = "hive.process"
HIVE_ADMIN = "hive.admin"
FILESYSTEM_WRITE = "filesystem.write"
NETWORK = "network"
PROCESS_EXEC = "process.exec"


@dataclass(slots=True)
class CardRow:
    text: str
    label: str = ""
    tone: str = ""  # info, success, warning or error


@dataclass(slots=True)
class Link:
    title: str
    url: str


@dataclass(slots=True)
class Card:
    """What one of the plugin's declared cards shows; no rows and no links clear it."""
    card: str
    environment: str = ""
    pane: str = ""
    rows: list[CardRow] = field(default_factory=list)
    links: list[Link] = field(default_factory=list)
    ttl_seconds: float | None = None

    def params(self) -> dict[str, Any]:
        out: dict[str, Any] = {"card": self.card}
        if self.environment:
            out["environment"] = self.environment
        if self.pane:
            out["pane"] = self.pane
        if self.rows:
            out["rows"] = [{k: v for k, v in asdict(r).items() if v} for r in self.rows]
        if self.links:
            out["links"] = [asdict(link) for link in self.links]
        if self.ttl_seconds is not None:
            out["ttl"] = ttl_nanos(self.ttl_seconds)
        return out


@dataclass(slots=True)
class Status:
    """A declared tab-bar indicator's text; no text clears it."""
    indicator: str
    text: str = ""
    tone: str = ""
    ttl_seconds: float | None = None

    def params(self) -> dict[str, Any]:
        out: dict[str, Any] = {"indicator": self.indicator}
        if self.text:
            out["text"] = self.text
        if self.tone:
            out["tone"] = self.tone
        if self.ttl_seconds is not None:
            out["ttl"] = ttl_nanos(self.ttl_seconds)
        return out


def ttl_nanos(seconds: float) -> int:
    """Seconds as the wire's nanosecond duration."""
    return round(seconds * 1_000_000_000)


@dataclass(slots=True)
class ExtensionContribution:
    """Deprecated: declare contributions in hive-plugin.toml [contributes] (plugin API 1)."""
    commands: list[dict[str, str]] = field(default_factory=list)
    views: list[dict[str, str]] = field(default_factory=list)
    keybindings: list[dict[str, str]] = field(default_factory=list)

    def merge(self, other: "ExtensionContribution") -> "ExtensionContribution":
        merged = ExtensionContribution(self.commands + other.commands, self.views + other.views, self.keybindings + other.keybindings)
        for kind, values in (("commands", merged.commands), ("views", merged.views), ("keybindings", merged.keybindings)):
            ids = [value.get("id", value.get("key", "")) for value in values]
            if len(ids) != len(set(ids)):
                raise ValueError(f"duplicate plugin {kind} contribution ID")
        return merged


__all__ = [
    "Card", "CardRow", "ExtensionContribution", "Link", "Status", "ttl_nanos",
    "HIVE_READ", "HIVE_WRITE", "HIVE_INPUT", "HIVE_PROCESS", "HIVE_ADMIN", "FILESYSTEM_WRITE", "NETWORK", "PROCESS_EXEC",
]
