#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# One shim freezes under a running daemon. Commands about other agents, and
# the daemon as a whole, must stay quick; a command about the frozen agent
# may wait, but must give up rather than hang. Then it thaws, cleanly.
begin frozen-shim-live
fixture
read -r id shim agent _ < <(run_records | awk '$4=="running"' | head -1)
other=$(hive --json pane list | jq -r --arg id "$id" '[.[]|select(.process_id!=$id)][0].process_id')
metric victim "$id"
safe_kill STOP "$shim"
timed() { # timed NAME LIMIT_S CMD... : run, record seconds, PASS if under LIMIT
	local name=$1 limit=$2 t0 rc; shift 2
	t0=$(date +%s%N)
	HIVE_HOME="$SB" HIVE_CONFIG="$SB/config.toml" timeout 30 "$HIVE_BIN" "$@" >/dev/null 2>&1 && rc=0 || rc=$?
	local ms=$((($(date +%s%N) - t0) / 1000000))
	metric "ms_$name" "$ms"
	if ((rc == 124)); then check FAIL "$name" "hung (>30s)"
	elif ((ms <= limit * 1000)); then check PASS "$name" "${ms}ms exit $rc"
	else check FAIL "$name" "${ms}ms exit $rc (limit ${limit}s)"; fi
}
timed "pane list while a shim is frozen" 2 pane list
timed "snapshot of another agent" 2 terminal snapshot "$other"
timed "debug stats" 2 --json debug stats
timed "snapshot of the frozen agent gives up" 15 terminal snapshot "$id"
timed "pane list after that" 2 pane list
timed "stop the frozen agent's pane gives up or works" 15 process stop "$id"
safe_kill CONT "$shim" 2>/dev/null || true
sleep 2
EXPECT_PANES=any verify_clean "frozen shim (live)" no any
verify_no_leak
