#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# The disk under HIVE_HOME fills up (a 32 MB tmpfs, so only it can fill).
# The daemon must keep serving, keep agents running, survive a crash while
# full, and recover once space is freed, without corrupting its records.
begin disk-full
mount_sandbox_tmpfs "${DISK_SIZE:-32m}"
fixture
dd if=/dev/zero of="$SB/filler" bs=1M count=1024 status=none 2>/dev/null || true
metric disk_free_kb_after_fill "$(df -Pk "$SB" | awk 'NR==2{print $4}')"
sleep 2
hive_ok pane list && check PASS "serves while full" "" || check FAIL "serves while full" ""
# Captured in memory: the sandbox has no room for it.
out=$(hive tab create dev -- sh -c "$TICK" 2>&1) && rc=0 || rc=$?
check INFO "start an agent while full" "exit $rc: $(head -c 160 <<<"$out" | tr '\n' ' ')"
[[ $rc == 0 || -n $out ]] && check PASS "a refused start says why" "" || check FAIL "a refused start says why" "exit $rc with no message"
pid=$(daemon_pid); safe_kill KILL "$pid"; wait_dead "$pid" || die "daemon survived SIGKILL"
if wait_serving "crash while full"; then check PASS "restarts while full" "${RECOVERY_MS}ms"
else check INFO "restarts while full" "not while full; freeing space"; fi
rm -f "$SB/filler"
sleep 1
# Records must still parse after the full disk.
bad=0
while IFS= read -r f; do jq -e . "$f" >/dev/null 2>&1 || { bad=$((bad + 1)); check FAIL "record parses" "${f#$SB/}"; }; done < <(find "$SB" -name '*.json' -size +0)
((bad == 0)) && check PASS "records intact" "" || true
empty=$(find "$SB" -name '*.json' -size 0 | sed "s|$SB/||" | tr '\n' ' ')
[[ -z $empty ]] && check PASS "no empty records" "" || check FAIL "no empty records" "$empty"
EXPECT_PANES=any verify_clean "disk full" any any
verify_no_leak
