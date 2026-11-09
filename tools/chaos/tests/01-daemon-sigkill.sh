#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# The daemon is SIGKILLed (a crash). The next command must autostart a new
# daemon that adopts every running agent through its shim.
begin daemon-sigkill
fixture
pid=$(daemon_pid)
safe_kill KILL "$pid"
wait_dead "$pid" || check FAIL "daemon died" "still alive"
verify_clean "daemon SIGKILL" yes
verify_no_leak
