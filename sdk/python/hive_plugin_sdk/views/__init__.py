from dataclasses import dataclass, field


@dataclass(slots=True)
class ExtensionContribution:
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


__all__ = ["ExtensionContribution"]
