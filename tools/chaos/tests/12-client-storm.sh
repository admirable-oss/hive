#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# ${STORM_CLIENTS:-200} clients hit the daemon at once (list, snapshot, stats).
# Every call must succeed and the daemon must not keep what they leave.
begin client-storm
fixture
ids=($(hive --json pane list | jq -r '.[].process_id'))
n=${STORM_CLIENTS:-200}
out="$SB/storm"; mkdir -p "$out"
for ((i = 0; i < n; i++)); do
	case $((i % 3)) in
		0) ( hive pane list >/dev/null 2>"$out/$i.err"; echo $? >"$out/$i.rc" ) & ;;
		1) ( hive terminal snapshot "${ids[i % ${#ids[@]}]}" >/dev/null 2>"$out/$i.err"; echo $? >"$out/$i.rc" ) & ;;
		2) ( hive --json debug stats >/dev/null 2>"$out/$i.err"; echo $? >"$out/$i.rc" ) & ;;
	esac
	((i % 50 == 49)) && watchdog
done
wait
bad=$(grep -LxF 0 "$out"/*.rc | wc -l)
metric storm_clients "$n"; metric storm_failures "$bad"
((bad == 0)) && check PASS "every storm call succeeded" "$n calls" ||
	check FAIL "every storm call succeeded" "$bad/$n failed: $(cat "$out"/*.err | sort | uniq -c | sort -rn | head -1 | cut -c1-120)"
watchdog
verify_clean "client storm" no
verify_no_leak
