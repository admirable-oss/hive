#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# The terminal that started everything hangs up: SIGHUP to the daemon, every
# shim and every agent's group leader's shim. Hive must shrug it off.
begin sighup
fixture
safe_kill HUP "$(daemon_pid)"
while read -r id shim agent st; do [[ $st == running ]] && safe_kill HUP "$shim"; done < <(run_records)
sleep 2
verify_clean "SIGHUP" no
verify_no_leak
