import importlib.util
import json
import pathlib
import tempfile
import unittest


SCRIPT = pathlib.Path(__file__).with_name("validate-plugin-submissions.py")
SPEC = importlib.util.spec_from_file_location("submission_validator", SCRIPT)
VALIDATOR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VALIDATOR)


def record():
    return {
        "schema": 1,
        "plugin_id": "sample-plugin",
        "state": "submitted",
        "listing": {
            "id": "sample-plugin", "name": "Sample", "license": "MIT",
            "repository": "https://github.com/example/sample",
            "maintainers": [{"name": "Example"}], "releases": [{"version": "1.0.0"}],
        },
        "created_at": "2026-01-01T00:00:00Z",
        "updated_at": "2026-01-01T00:00:00Z",
        "history": [{"at": "2026-01-01T00:00:00Z", "actor": "example", "action": "submitted"}],
    }


class SubmissionValidatorTests(unittest.TestCase):
    def validate(self, value):
        with tempfile.TemporaryDirectory() as temp:
            path = pathlib.Path(temp) / "sample-plugin.json"
            path.write_text(json.dumps(value), encoding="utf-8")
            VALIDATOR.validate(path)

    def test_valid_submitted_record(self):
        self.validate(record())

    def test_rejects_mismatched_listing_id(self):
        value = record()
        value["listing"]["id"] = "other-plugin"
        with self.assertRaisesRegex(ValueError, "listing.id"):
            self.validate(value)

    def test_rejects_invalid_review_transition(self):
        value = record()
        value["state"] = "approved"
        value["history"].append({"at": "2026-01-02T00:00:00Z", "actor": "reviewer", "action": "archived"})
        with self.assertRaisesRegex(ValueError, "invalid state transition"):
            self.validate(value)


if __name__ == "__main__":
    unittest.main()
