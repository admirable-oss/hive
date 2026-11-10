#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# The daemon is SIGKILLed ${STORM:-10} times in a row, each time as soon as a
# new one serves. Every restart must adopt the agents; nothing may pile up.
begin daemon-kill-storm
fixture
worst=0
for ((n = 1; n <= ${STORM:-10}; n++)); do
	pid=$(daemon_pid)
	safe_kill KILL "$pid"
	wait_dead "$pid" || die "daemon $pid survived SIGKILL"
	wait_serving "kill $n" || { check FAIL "restart $n" "not serving"; break; }
	((RECOVERY_MS > worst)) && worst=$RECOVERY_MS
	watchdog
done
metric storm_kills "${STORM:-10}"; metric worst_recovery_ms "$worst"
check INFO "storm finished" "${STORM:-10} kills, worst recovery ${worst}ms"
verify_clean "kill storm" yes
verify_no_leak
