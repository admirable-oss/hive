#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# The daemon crashes and its own records are damaged: one agent's
# process.json and the environment's layout.json are truncated. The next
# daemon must start, keep every agent running, adopted and shown (lost
# panes come back in a "recovered" tab), and leave none unsupervised.
begin corrupt-daemon-records
fixture
id=$(hive --json pane list | jq -r '.[0].process_id')
read -r _ shim agent _ < <(run_records | awk -v id="$id" '$1==id')
metric victim "$id shim=$shim agent=$agent"
pid=$(daemon_pid); safe_kill KILL "$pid"; wait_dead "$pid" || die "daemon survived SIGKILL"
rec=$(find "$SB/environments" -path "*/processes/$id/process.json")
printf '{"id":"%s","sta' "$id" >"$rec"
printf '{"schema":1,"tab' >"$SB/environments/dev/layout.json"
if wait_serving "corrupt records"; then check PASS "daemon starts over corrupt records" "${RECOVERY_MS}ms"
else check FAIL "daemon starts over corrupt records" "not within ${RECOVERY_TIMEOUT}s"; fi
sleep 2
ls "$SB/environments/dev/"layout.json.corrupt-* >/dev/null 2>&1 && check PASS "bad layout moved aside" "" || check INFO "bad layout moved aside" "no backup"
panes=$(hive --json pane list | jq '. // []')
procs=$(hive --json process list 2>/dev/null | jq '. // []')
check INFO "processes the daemon knows" "$(jq -c '[.[]|{id:.id[0:6],status,for_pane}]' <<<"$procs")"
live_agents=$(run_records | awk '{print $3}' | while read -r a; do owned "$a" && echo "$a"; done | wc -l)
metric agents_alive_after "$live_agents"
check INFO "agents alive" "$live_agents of $AGENTS"
metric panes_after "$(jq length <<<"$panes")"
vst=$(jq -r --arg id "$id" '[.[]|select(.process_id==$id)][0].process.status // "<no pane>"' <<<"$panes")
check INFO "victim's pane" "$vst"
if owned "$agent"; then
	if [[ $vst == running ]]; then check PASS "victim adopted" ""
	else check FAIL "victim not left unsupervised" "agent $agent runs but the daemon shows its pane as $vst"; fi
else
	check PASS "victim not left running unsupervised" "stopped"
fi
((live_agents == AGENTS)) && check PASS "no agent lost to bad records" "$live_agents/$AGENTS" || check FAIL "no agent lost to bad records" "$live_agents/$AGENTS alive"
rec_tabs=$(hive --json tab list 2>/dev/null | jq '[.[]? | select(.name == "recovered")] | length')
check INFO "recovered tabs" "${rec_tabs:-?}"
verify_clean "corrupt daemon records" yes
verify_no_leak
