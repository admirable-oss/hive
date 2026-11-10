#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# One shim is frozen (SIGSTOP: swapped out, stuck in the kernel) while the
# daemon restarts. The new daemon must not wedge on it, and must not kill a
# live agent just because its shim was slow to answer.
begin frozen-shim-restart
fixture
read -r id shim agent _ < <(run_records | awk '$4=="running"' | head -1)
metric victim "$id shim=$shim agent=$agent"
safe_kill STOP "$shim"
pid=$(daemon_pid); safe_kill KILL "$pid"; wait_dead "$pid" || die "daemon survived SIGKILL"
if wait_serving "restart with a frozen shim"; then
	check PASS "daemon serves with a frozen shim" "${RECOVERY_MS}ms"
	metric startup_ms_frozen_shim "$RECOVERY_MS"
else
	check FAIL "daemon serves with a frozen shim" "not within ${RECOVERY_TIMEOUT}s"
fi
st=$(hive --json pane list | jq -r --arg id "$id" '[.[]|select(.process_id==$id)][0].process.status // "<replaced>"')
metric victim_status_while_frozen "$st"
owned "$agent" && check PASS "agent alive while its shim is frozen" "status $st" ||
	check FAIL "agent alive while its shim is frozen" "agent $agent was killed (status $st)"
safe_kill CONT "$shim" 2>/dev/null || true
sleep 3
if owned "$shim" && ! owned "$agent"; then check FAIL "no shim left without its agent" "shim $shim alive, agent gone"; fi
verify_clean "frozen shim" yes any
verify_no_leak
