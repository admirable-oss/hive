#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# The daemon crashes and its pidfile is left pointing at an unrelated live
# process (PID reuse). Hive must start a new daemon and must not signal the
# decoy.
begin stale-pidfile-decoy
fixture
pid=$(daemon_pid)
safe_kill KILL "$pid"
wait_dead "$pid" || die "daemon survived SIGKILL"
# The decoy is ours (not the sandbox's), so the harness never signals it
# through safe_kill; it is reaped below.
sleep 600 & decoy=$!
echo "$decoy" >"$SB/hive.pid"
metric decoy_pid "$decoy"
verify_clean "stale pidfile" any
alive "$decoy" && check PASS "decoy left alone" "pid $decoy" || check FAIL "decoy left alone" "decoy was killed"
kill "$decoy" 2>/dev/null || true; wait "$decoy" 2>/dev/null || true
verify_no_leak
