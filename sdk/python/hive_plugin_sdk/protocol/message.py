from dataclasses import dataclass, field
from typing import Any


@dataclass(slots=True)
class Envelope:
    type: str
    version: str = "2"
    id: str = ""
    method: str = ""
    params: Any = None
    result: Any = None
    error: dict[str, str] | None = None
    stream: str | None = None
    data: str | None = None


@dataclass(slots=True)
class Hello:
    client: str
    client_version: str
    capabilities: list[str] = field(default_factory=list)


@dataclass(slots=True)
class Welcome:
    protocol: str
    server_version: str | None = None
    methods: list[str] = field(default_factory=list)
    capabilities: list[str] = field(default_factory=list)
