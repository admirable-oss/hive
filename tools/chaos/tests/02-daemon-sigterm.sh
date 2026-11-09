#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# The daemon is asked to exit (SIGTERM, as a service manager would). Agents
# must keep running and the next daemon must adopt them.
begin daemon-sigterm
fixture
pid=$(daemon_pid)
safe_kill TERM "$pid"
wait_dead "$pid" 15 && check PASS "daemon exited on SIGTERM" "" || check FAIL "daemon exited on SIGTERM" "alive after 15s"
verify_clean "daemon SIGTERM" yes
verify_no_leak
