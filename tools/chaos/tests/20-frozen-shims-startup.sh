#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# Several shims are frozen when the daemon restarts (a machine under heavy
# memory pressure). Each one that does not answer costs a dial timeout; the
# daemon must not make the user wait for them one after another.
begin frozen-shims-startup
fixture
n=${FROZEN:-3}
mapfile -t victims < <(run_records | awk '$4=="running"{print $2" "$3}' | head -"$n")
for v in "${victims[@]}"; do safe_kill STOP "${v%% *}"; done
pid=$(daemon_pid); safe_kill KILL "$pid"; wait_dead "$pid" || die "daemon survived SIGKILL"
if wait_serving "restart with $n frozen shims"; then
	metric startup_ms "$RECOVERY_MS"
	((RECOVERY_MS <= ${STARTUP_LIMIT_MS:-5000})) && check PASS "serves soon with $n frozen shims" "${RECOVERY_MS}ms" ||
		check FAIL "serves soon with $n frozen shims" "${RECOVERY_MS}ms (limit ${STARTUP_LIMIT_MS:-5000}ms)"
else
	check FAIL "serves soon with $n frozen shims" "not within ${RECOVERY_TIMEOUT}s"
fi
alive=0; for v in "${victims[@]}"; do owned "${v##* }" && alive=$((alive + 1)); done
((alive == n)) && check PASS "frozen agents kept" "$alive/$n" || check FAIL "frozen agents kept" "$alive/$n"
for v in "${victims[@]}"; do safe_kill CONT "${v%% *}" 2>/dev/null || true; done
sleep 4 # the retry re-attaches them
verify_clean "frozen shims at startup" yes
verify_no_leak
