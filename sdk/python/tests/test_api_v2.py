import json
import unittest
from pathlib import Path

from hive_plugin_sdk import PluginContext
from hive_plugin_sdk import views

V2 = Path(__file__).resolve().parents[2] / "protocol" / "fixtures" / "v2"


class APIv2Tests(unittest.TestCase):
    def test_contexts_and_capabilities(self) -> None:
        action = PluginContext.from_dict(json.loads((V2 / "context-action.json").read_text()))
        self.assertEqual(action.api, 2)
        self.assertTrue(action.has(views.FILESYSTEM_READ_WORKSPACE))
        self.assertTrue(action.has(views.UNCONFINED))
        discovery = json.loads((V2 / "capabilities.json").read_text())
        names = {c["name"] for c in discovery["capabilities"]}
        for name in ("HIVE_READ", "HIVE_WRITE", "HIVE_INPUT", "HIVE_PROCESS", "HIVE_ADMIN", "FILESYSTEM_WRITE", "NETWORK",
                     "PROCESS_EXEC", "FILESYSTEM_READ", "FILESYSTEM_READ_WORKSPACE", "FILESYSTEM_WRITE_WORKSPACE", "UNCONFINED"):
            self.assertIn(getattr(views, name), names)


if __name__ == "__main__":
    unittest.main()
