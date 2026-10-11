#!/bin/sh
# pr-status: put each environment's pull request in Hive's sidebar.
#
#   pr-status.sh watch   every $PR_STATUS_INTERVAL seconds (default 60)
#   pr-status.sh once    one pass over every environment
#   pr-status.sh open    open the current environment's PR in the browser
#
# An environment's PR is the one gh finds for the branch its directory has
# checked out. The token reads "#12 open ✓": number, state, and its checks
# (✓ passed, ✗ failed, … running). It expires unless refreshed, so stopping
# the plugin leaves nothing stale behind.
set -u
hive=${HIVE_BIN:-hive}
interval=${PR_STATUS_INTERVAL:-60}

# The token for one PR, computed by gh's built-in jq.
summary='"#\(.number) " +
  (if .state == "MERGED" then "merged" elif .state == "CLOSED" then "closed" elif .isDraft then "draft" else "open" end) +
  (if (.statusCheckRollup | length) == 0 then ""
   elif any(.statusCheckRollup[]; (.conclusion // .state) == "FAILURE" or (.conclusion // .state) == "ERROR") then " ✗"
   elif all(.statusCheckRollup[]; (.conclusion // .state) == "SUCCESS" or (.conclusion // .state) == "SKIPPED" or (.conclusion // .state) == "NEUTRAL") then " ✓"
   else " …" end)'

# one ENV DIR reports the PR of the branch checked out in DIR.
one() {
	branch=$(git -C "$2" branch --show-current 2>/dev/null) || return 0
	[ -n "$branch" ] || return 0
	if token=$(cd "$2" && gh pr view "$branch" --json number,state,isDraft,statusCheckRollup --jq "$summary" 2>/dev/null) && [ -n "$token" ]; then
		"$hive" env report-metadata "$1" --token "pr=$token" --ttl $((interval * 3))s >/dev/null
	else
		"$hive" env report-metadata "$1" --token pr= >/dev/null 2>&1 || true
	fi
}

pass() {
	"$hive" env list --template '{{range .}}{{.id}}	{{.path}}{{"\n"}}{{end}}' |
		while IFS='	' read -r env dir; do
			[ -n "$env" ] && one "$env" "$dir"
		done
}

# The environment an action was run in: from its context.
context_env() {
	sed -n 's/.*"environment":"\([^"]*\)".*/\1/p' "${HIVE_PLUGIN_CONTEXT:-/dev/null}"
}

case "${1:-once}" in
watch)
	while :; do
		pass
		sleep "$interval"
	done
	;;
once)
	pass
	echo "PR tokens refreshed"
	;;
open)
	env=$(context_env)
	[ -n "$env" ] || { echo "no environment to open a PR for" >&2; exit 1; }
	dir=$("$hive" env get "$env" --template '{{.path}}')
	cd "$dir" && gh pr view --web
	;;
*)
	echo "usage: pr-status.sh watch|once|open" >&2
	exit 2
	;;
esac
