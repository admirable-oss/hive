#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# One agent's shim is SIGKILLed. The daemon must survive, record that agent
# as gone (not leave it running or "starting"), and not leave its agent
# orphaned; the other agents must not notice.
begin shim-sigkill
fixture
read -r id shim agent _ < <(run_records | awk '$4=="running"' | head -1)
metric victim "$id shim=$shim agent=$agent"
safe_kill KILL "$shim"
wait_dead "$shim" || check FAIL "shim died" ""
if wait_dead "$agent" 10; then check PASS "victim agent did not outlive its shim" ""
else check FAIL "victim agent did not outlive its shim" "agent $agent orphaned"; fi
sleep 1
st=$(hive --json pane list | jq -r --arg id "$id" '.[] | select(.process_id==$id) | .process.status')
metric victim_status "$st"
[[ $st != running && $st != starting ]] && check PASS "victim recorded as ended" "$st" || check FAIL "victim recorded as ended" "$st"
verify_clean "shim SIGKILL" no $((AGENTS - 1))
verify_no_leak
