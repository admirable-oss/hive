#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# The daemon crashes and one agent's state.json is truncated to garbage
# (a crash mid-write, a full disk). The next daemon must start, keep the
# other agents, and not wedge on the bad record.
begin corrupt-state
fixture
read -r id shim agent _ < <(run_records | awk '$4=="running"' | head -1)
metric victim "$id"
pid=$(daemon_pid)
safe_kill KILL "$pid"
wait_dead "$pid" || die "daemon survived SIGKILL"
printf '{"schema":1,"stat' >"$SB/run/$id/state.json"
if wait_serving "corrupt record"; then
	check PASS "daemon starts over a corrupt record" "${RECOVERY_MS}ms"
	st=$(hive --json pane list | jq -r --arg id "$id" '.[] | select(.process_id==$id) | .process.status')
	metric victim_status "${st:-<missing>}"
	check INFO "victim pane status" "${st:-<missing>} (shim $shim alive: $(owned "$shim" && echo yes || echo no))"
fi
# The victim's shim rewrites state.json on its next change, so it may be
# adopted again; either way the rest must be clean.
live=$(hive --json pane list | jq '[.[]|select(.process.status=="running")]|length')
((live >= AGENTS - 1)) && check PASS "other agents kept" "$live running" || check FAIL "other agents kept" "$live running"
verify_clean "corrupt state" yes "$live"
verify_no_leak
