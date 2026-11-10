#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# The daemon runs out of file descriptors (a client storm against a low
# RLIMIT_NOFILE). Calls may fail while it is starved, but once descriptors
# are free again the same daemon must accept connections: running out is
# temporary, and must not stop it from serving for good.
begin fd-exhaustion
fixture
pid=$(daemon_pid)
used=$(find "/proc/$pid/fd" -mindepth 1 -maxdepth 1 | wc -l)
orig=$(prlimit --pid "$pid" --nofile --noheadings --output SOFT,HARD | awk '{print $1":"$2}')
limit=$((used + ${FD_HEADROOM:-6}))
metric fds_in_use "$used"; metric nofile_limit_during "$limit"
safe_prlimit "$pid" --nofile="$limit:${orig#*:}"
out="$SB/storm"; mkdir -p "$out"
for ((i = 0; i < ${FD_CLIENTS:-60}; i++)); do
	( hive pane list >/dev/null 2>&1; echo $? >"$out/$i.rc" ) &
done
wait
failed=$(grep -LxF 0 "$out"/*.rc | wc -l)
metric calls_failed_while_starved "$failed"
check INFO "calls while starved" "$failed/${FD_CLIENTS:-60} failed"
safe_prlimit "$pid" --nofile="$orig"
sleep 1
if timeout 10 bash -c 'until HIVE_HOME="$1" HIVE_CONFIG="$1/config.toml" "$2" pane list >/dev/null 2>&1; do sleep 0.3; done' _ "$SB" "$HIVE_BIN"; then
	check PASS "serves again once descriptors are free" ""
else
	check FAIL "serves again once descriptors are free" "$(grep -iE 'accept' "$SB/logs/daemon.log" | tail -1 | sed -E 's/^time=[^ ]+ //' | cut -c1-160)"
fi
[[ $(daemon_pid) == "$pid" ]] && owned "$pid" && check PASS "same daemon" "$pid" || check INFO "same daemon" "$pid -> $(daemon_pid)"
verify_clean "fd exhaustion" any
verify_no_leak
