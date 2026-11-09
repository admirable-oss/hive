#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# The daemon is frozen (SIGSTOP) for ${FREEZE_SECS:-5}s, then resumed. It must
# be the same daemon afterwards, serving, with no second daemon started by
# a client that timed out meanwhile.
begin daemon-freeze
fixture
pid=$(daemon_pid)
safe_kill STOP "$pid"
# A client hitting the frozen daemon must not start a second one.
( hive pane list >/dev/null 2>&1 || true ) &
sleep "${FREEZE_SECS:-5}"
safe_kill CONT "$pid"
wait || true
verify_clean "daemon freeze" no
verify_no_leak
