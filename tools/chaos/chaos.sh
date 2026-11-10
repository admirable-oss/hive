#!/usr/bin/env bash
# chaos.sh: run hive daemon chaos tests against throwaway sandboxes.
#
#   tools/chaos/chaos.sh list            the tests
#   tools/chaos/chaos.sh run 01 04       some tests, in order
#   tools/chaos/chaos.sh all             every test, in order
#
# Each test prints its record (also written to $CHAOS_ROOT/records) before
# the next starts. A guardrail abort (exit 3) stops the run. Thresholds and
# knobs are environment variables; see lib.sh and README.md.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
tests() { ls "$here"/tests/*.sh; }

run() {
	local t rc failed=0
	for t in "$@"; do
		echo "================ $(basename "$t" .sh) ================"
		bash "$t"; rc=$?
		case $rc in
			0) ;;
			3) echo "guardrail tripped in $(basename "$t"); stopping the run" >&2; return 3 ;;
			*) failed=1 ;;
		esac
	done
	return $failed
}

case ${1:-help} in
	list) tests | xargs -n1 basename ;;
	all) run $(tests) ;;
	run) shift; sel=(); for p in "$@"; do sel+=("$(ls "$here"/tests/"$p"*.sh | head -1)"); done; run "${sel[@]}" ;;
	*) sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//' ;;
esac
