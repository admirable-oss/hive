#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# A /tmp cleaner (systemd-tmpfiles) deletes a long-running shim's socket;
# later the daemon restarts. The agent must be re-attached, not left
# running out of reach.
begin shim-socket-cleaned
fixture
read -r id shim agent _ < <(run_records | awk '$4=="running"' | head -1)
sock=$(sock_path "$SB/run/$id" shim.sock)
metric socket_location "$([[ $sock == /tmp/hive-* ]] && echo hashed || echo in-run-dir)"
rm -f "$sock"
sleep "${SHIM_SOCK_WAIT:-4}"
pid=$(daemon_pid); safe_kill KILL "$pid"; wait_dead "$pid" || die "daemon survived SIGKILL"
wait_serving "restart" || check FAIL "serving after restart" ""
sleep 3
st=$(hive --json pane list | jq -r --arg id "$id" '[.[]|select(.process_id==$id)][0].process.status // "<none>"')
snap=$(timeout 15 env HIVE_HOME="$SB" HIVE_CONFIG="$SB/config.toml" "$HIVE_BIN" terminal snapshot "$id" 2>&1 | grep -c 'tick' || true)
check INFO "victim after restart" "status $st, agent alive: $(owned "$agent" && echo yes || echo no)"
((snap > 0)) && check PASS "victim reachable again" "its screen reads" || check FAIL "victim reachable again" "status $st but its terminal cannot be read"
verify_clean "shim socket cleaned" yes
verify_no_leak
