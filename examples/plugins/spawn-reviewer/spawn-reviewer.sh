#!/bin/sh
# spawn-reviewer: on worktree.created, start a reviewer agent in the new
# worktree's environment and ask it to review the branch.
set -eu
hive=${HIVE_BIN:-hive}
kind=${SPAWN_REVIEWER_KIND:-claude}

# The event's context: the new environment, and its branch.
ctx=${HIVE_PLUGIN_CONTEXT:?run by hive on worktree.created}
env=$(sed -n 's/.*"environment":"\([^"]*\)".*/\1/p' "$ctx")
branch=$(sed -n 's/.*"branch":"\([^"]*\)".*/\1/p' "$ctx")
[ -n "$env" ] || { echo "the event names no environment" >&2; exit 1; }

agent=$("$hive" agent start --kind "$kind" --env "$env" --name reviewer --template '{{.agent.id}}')
echo "started reviewer $agent in $env"

"$hive" agent prompt "$agent" "Review the changes on branch ${branch:-$env} against the branch it started from. List problems by severity with file and line; do not change any files."
echo "asked $agent to review ${branch:-$env}"
