#!/usr/bin/env python3
"""Dependency-free validation for the checked-in plugin submission ledger."""

import datetime
import json
import os
import pathlib
import re
import subprocess
import sys
import urllib.error
import urllib.request


ALLOWED_STATES = {"draft", "submitted", "changes_requested", "approved", "rejected", "withdrawn", "archived"}
ALLOWED_ACTIONS = {
    "submitted", "changes_requested", "approved", "rejected",
    "ownership_transfer_requested", "ownership_transferred", "reported",
    "appealed", "withdrawn", "archived",
}
TRANSITIONS = {
    "draft": {"submitted", "withdrawn"},
    "submitted": {"changes_requested", "approved", "rejected", "withdrawn"},
    "changes_requested": {"submitted", "withdrawn"},
    "approved": {"approved", "withdrawn", "archived"},
    "rejected": {"submitted", "withdrawn"},
    "withdrawn": {"archived"},
    "archived": set(),
}


def fail(path, message):
    raise ValueError(f"{path}: {message}")


def timestamp(value, path):
    if not isinstance(value, str):
        fail(path, "expected an ISO-8601 timestamp")


def base_record(plugin_id):
    base = os.environ.get("BASE_REF")
    if not base:
        return None
    result = subprocess.run(
        ["git", "show", f"origin/{base}:registry/submissions/{plugin_id}.json"],
        capture_output=True, text=True, check=False,
    )
    if result.returncode:
        return None
    return json.loads(result.stdout)


def verify_transfer_approvals(record, path):
    transfer = record.get("transfer")
    repository = os.environ.get("GITHUB_REPOSITORY")
    pull_number = os.environ.get("PR_NUMBER")
    token = os.environ.get("GITHUB_TOKEN")
    if transfer is None or not (repository and pull_number and token):
        return
    request = urllib.request.Request(
        f"https://api.github.com/repos/{repository}/pulls/{pull_number}/reviews",
        headers={"Accept": "application/vnd.github+json", "Authorization": f"Bearer {token}"},
    )
    try:
        with urllib.request.urlopen(request, timeout=10) as response:
            reviews = json.load(response)
    except (OSError, urllib.error.URLError, json.JSONDecodeError) as error:
        fail(path, f"could not verify ownership-transfer approvals: {error}")
    latest = {}
    for review in reviews:
        login = (review.get("user") or {}).get("login", "").casefold()
        if login:
            latest[login] = review.get("state", "").upper()
    required = {login.casefold() for login in [*transfer.get("from", []), *transfer.get("to", [])]}
    missing = sorted(login for login in required if latest.get(login) != "APPROVED")
    if not transfer.get("from") or not transfer.get("to") or missing:
        fail(path, f"ownership transfer requires approvals from outgoing and incoming maintainers; missing {', '.join(missing) or 'maintainer identities'}")
    try:
        datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        fail(path, "expected an ISO-8601 timestamp")


def validate(path):
    record = json.loads(path.read_text(encoding="utf-8"))
    if record.get("schema") != 1:
        fail(path, "schema must be 1")
    plugin_id = record.get("plugin_id", "")
    if not re.fullmatch(r"[a-z][a-z0-9-]{1,62}", plugin_id):
        fail(path, "plugin_id is malformed")
    if path.stem != plugin_id:
        fail(path, "filename must be <plugin_id>.json")
    state = record.get("state")
    if state not in ALLOWED_STATES:
        fail(path, f"unknown submission state {state!r}")
    timestamp(record.get("created_at"), f"{path}: created_at")
    timestamp(record.get("updated_at"), f"{path}: updated_at")
    listing = record.get("listing")
    if not isinstance(listing, dict) or listing.get("id") != plugin_id:
        fail(path, "listing.id must match plugin_id")
    if not listing.get("name") or not listing.get("license") or not listing.get("releases"):
        fail(path, "listing needs a name, license, and at least one release")
    maintainers = listing.get("maintainers")
    if not isinstance(maintainers, list) or not maintainers or any(not m.get("name") for m in maintainers):
        fail(path, "listing needs at least one named maintainer")
    history = record.get("history")
    if not isinstance(history, list) or not history:
        fail(path, "history must be a non-empty append-only event list")
    current = "draft"
    state_events = {"submitted", "changes_requested", "approved", "rejected", "withdrawn", "archived"}
    for index, event in enumerate(history):
        if event.get("action") not in ALLOWED_ACTIONS or not event.get("actor"):
            fail(path, f"history[{index}] needs a known action and actor")
        timestamp(event.get("at"), f"{path}: history[{index}].at")
        action = event["action"]
        if action in state_events:
            if action not in TRANSITIONS[current]:
                fail(path, f"invalid state transition {current} -> {action} at history[{index}]")
            current = action
    if current != state:
        fail(path, "state must match the latest state-changing history event")
    if state == "approved" and not any(event.get("action") == "approved" for event in history):
        fail(path, "approved submissions need an approval event")
    if state == "changes_requested" and history[-1].get("action") != "changes_requested":
        fail(path, "changes_requested must be the latest state-changing event")
    transfer = record.get("transfer")
    if transfer is not None and (not transfer.get("outgoing_approval") or not transfer.get("incoming_acceptance")):
        fail(path, "ownership transfers need outgoing approval and incoming acceptance")
    if transfer is not None and "ownership_transferred" not in [event.get("action") for event in history]:
        fail(path, "completed ownership transfer needs an audit event")
    previous = base_record(plugin_id)
    if previous:
        if record.get("created_at") != previous.get("created_at"):
            fail(path, "created_at is immutable")
        old_history = previous.get("history", [])
        if history[:len(old_history)] != old_history:
            fail(path, "history is append-only; existing audit events cannot be rewritten or removed")
        old_releases = previous.get("listing", {}).get("releases", [])
        if listing.get("releases", [])[:len(old_releases)] != old_releases:
            fail(path, "release metadata is append-only; existing releases cannot be rewritten or removed")
        old_maintainers = previous.get("listing", {}).get("maintainers", [])
        if old_maintainers != maintainers and transfer is None:
            fail(path, "maintainer ownership changes require a transfer record")
        if transfer is not None and old_maintainers:
            old_logins = {m.get("github_login", "").casefold() for m in old_maintainers}
            new_logins = {m.get("github_login", "").casefold() for m in maintainers}
            stated_from = {login.casefold() for login in transfer.get("from", [])}
            stated_to = {login.casefold() for login in transfer.get("to", [])}
            if not old_logins or "" in old_logins or not old_logins.issubset(stated_from):
                fail(path, "every outgoing maintainer needs a GitHub login recorded before transfer")
            if not new_logins or "" in new_logins or stated_to != new_logins:
                fail(path, "transfer.to must match the incoming maintainers' GitHub logins")
            if old_maintainers != maintainers:
                verify_transfer_approvals(record, path)
    for key, value in record.items():
        if key not in {"schema", "plugin_id", "state", "listing", "created_at", "updated_at", "history", "transfer", "appeal", "reports"}:
            fail(path, f"unknown field {key!r}")
    if state == "approved" and (record.get("listing", {}).get("official") or record.get("listing", {}).get("verified")):
        if not any(event.get("action") == "approved" and event.get("reference") for event in history):
            fail(path, "official and verified approval events need a review reference")


def main():
    root = pathlib.Path("registry/submissions")
    files = sorted(path for path in root.glob("*.json") if path.name != "schema-v1.json")
    for path in files:
        try:
            validate(path)
        except (OSError, ValueError, json.JSONDecodeError) as error:
            print(error, file=sys.stderr)
            return 1
    print(f"validated {len(files)} plugin submission record(s)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
