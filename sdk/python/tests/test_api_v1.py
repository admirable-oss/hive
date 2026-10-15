import json
import unittest
from pathlib import Path

from hive_plugin_sdk import Card, CardRow, Link, PluginContext, Status
from hive_plugin_sdk.views import HIVE_WRITE, NETWORK, ttl_nanos

V1 = Path(__file__).resolve().parents[2] / "protocol" / "fixtures" / "v1"


class APIv1Tests(unittest.TestCase):
    def test_contexts_from_shared_fixtures(self) -> None:
        action = PluginContext.from_dict(json.loads((V1 / "context-action.json").read_text()))
        self.assertEqual(action.api, 1)
        self.assertTrue(action.has(HIVE_WRITE))
        self.assertFalse(action.has(NETWORK))
        self.assertEqual((action.action, action.environment, action.pane), ("show", "api", "p1"))
        event = PluginContext.from_dict(json.loads((V1 / "context-event.json").read_text()))
        self.assertEqual((event.event.type, event.event.seq), ("worktree.created", 42))

    def test_card_and_status_params_match_the_wire(self) -> None:
        want = json.loads((V1 / "request-plugin-card.json").read_text())["params"]
        card = Card(card="build", environment="api", rows=[CardRow(label="main", text="passing", tone="success")],
                    links=[Link(title="Run", url="https://ci.example.com/run/1")], ttl_seconds=600)
        self.assertEqual(card.params(), want)
        self.assertEqual(Status(indicator="builds").params(), {"indicator": "builds"})
        self.assertEqual(ttl_nanos(1.5), 1_500_000_000)


if __name__ == "__main__":
    unittest.main()
