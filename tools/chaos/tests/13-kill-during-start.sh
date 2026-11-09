#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# The daemon is SIGKILLed while it is starting an agent, ${ROUNDS:-6} times at
# varying moments. Whatever got started must end up either running and
# adopted or recorded as ended: never stuck "starting", never orphaned.
begin kill-during-start
fixture
for ((r = 1; r <= ${ROUNDS:-6}; r++)); do
	( hive tab create dev -- sh -c "$TICK" >/dev/null 2>&1 || true ) &
	sleep "0.0$((RANDOM % 10))"
	pid=$(daemon_pid)
	owned "$pid" && safe_kill KILL "$pid"
	wait
	wait_serving "round $r" || { check FAIL "serving after round $r" ""; break; }
	watchdog
done
EXPECT_PANES=any verify_clean "kills during start" yes any
verify_no_leak
