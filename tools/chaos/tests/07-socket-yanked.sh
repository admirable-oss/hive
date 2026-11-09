#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# The control socket is deleted under a live daemon (a tmp cleaner, a bad
# rm). Clients must not hang and must not end up with two daemons fighting
# over the same agents.
begin socket-yanked
fixture
rm -f "$SB/hive.sock"
t0=$(date +%s)
out=$(hive pane list 2>&1) && rc=0 || rc=$?
metric first_cmd_exit "$rc"; metric first_cmd_secs "$(($(date +%s) - t0))"
check INFO "first command after rm" "exit $rc: $(head -1 <<<"$out" | cut -c1-120)"
((rc != 124)) && check PASS "client did not hang" "" || check FAIL "client did not hang" "timed out after 20s"
verify_clean "socket removed" any
verify_no_leak
