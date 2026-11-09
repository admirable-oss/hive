#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# ${SCALE_AGENTS:-24} agents, the daemon SIGKILLed: every one must be adopted,
# and how long that takes is recorded.
AGENTS=${SCALE_AGENTS:-24}
MAX_SANDBOX_PROCS=$((AGENTS * 3 + 8))
begin scale-restart
fixture
pid=$(daemon_pid); safe_kill KILL "$pid"; wait_dead "$pid" || die "daemon survived SIGKILL"
verify_clean "daemon SIGKILL with $AGENTS agents" yes
verify_no_leak
