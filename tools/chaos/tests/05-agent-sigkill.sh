#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# An agent process itself is SIGKILLed. Its shim must record the exit and
# go away; the daemon and other agents carry on.
begin agent-sigkill
fixture
read -r id shim agent _ < <(run_records | awk '$4=="running"' | head -1)
metric victim "$id shim=$shim agent=$agent"
safe_kill KILL "$agent"
wait_dead "$shim" 10 && check PASS "shim exited after its agent" "" || check FAIL "shim exited after its agent" "shim $shim lingers"
sleep 1
st=$(hive --json pane list | jq -r --arg id "$id" '.[] | select(.process_id==$id) | .process.status')
metric victim_status "$st"
[[ $st != running && $st != starting ]] && check PASS "victim recorded as ended" "$st" || check FAIL "victim recorded as ended" "$st"
verify_clean "agent SIGKILL" no $((AGENTS - 1))
verify_no_leak
