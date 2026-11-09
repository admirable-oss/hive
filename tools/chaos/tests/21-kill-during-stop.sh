#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# `hive stop` (stop the daemon and every agent) is interrupted by a SIGKILL
# of the daemon part-way. Whatever was stopped and whatever was not, the
# next daemon must account for every agent: running ones adopted, stopped
# ones recorded as ended, none orphaned or stuck active.
begin kill-during-stop
# Agents that take a moment to stop, so the stop is in progress.
TICK='trap "sleep 1; exit 0" TERM; i=0; while :; do i=$((i+1)); echo "tick $i"; sleep 0.2; done'
fixture
pid=$(daemon_pid)
( hive stop >/dev/null 2>&1 || true ) &
sleep "${STOP_KILL_AFTER:-0.4}"
owned "$pid" && safe_kill KILL "$pid"
wait
wait_dead "$pid" || die "daemon survived SIGKILL"
if wait_serving "a stop cut short"; then check PASS "next daemon serves" "${RECOVERY_MS}ms"; else check FAIL "next daemon serves" ""; fi
sleep 1
procs=$(hive --json process list | jq '. // []')
check INFO "agents after" "$(jq -c 'group_by(.status) | map({(.[0].status): length}) | add' <<<"$procs")"
active=$(jq '[.[]|select(.status=="running" or .status=="starting")]|length' <<<"$procs")
alive=0; while read -r _ _ a _; do owned "$a" && alive=$((alive + 1)); done < <(run_records)
metric records_active "$active"; metric agents_alive "$alive"
# Every agent still alive must be one the daemon supervises, and vice versa.
((active == alive)) && check PASS "records match reality" "$active active, $alive alive" ||
	check FAIL "records match reality" "$active records active, $alive agents alive"
EXPECT_PANES=any verify_clean "a stop cut short" yes any
verify_no_leak
