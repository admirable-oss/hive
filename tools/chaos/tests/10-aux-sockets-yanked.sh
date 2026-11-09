#!/usr/bin/env bash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
# The member and observer sockets are deleted under a live daemon. Like the
# control socket, they must come back rather than stay dead until a restart.
begin aux-sockets-yanked
fixture
for s in member observer; do
	if served "$(sock_path "$SB" "$s.sock")"; then check PASS "$s socket served before" ""; else check INFO "$s socket served before" "no (disabled?)"; fi
	rm -f "$(sock_path "$SB" "$s.sock")"
done
sleep "${AUX_WAIT:-6}"
for s in member observer; do
	if served "$(sock_path "$SB" "$s.sock")"; then check PASS "$s socket back" ""
	else check FAIL "$s socket back" "not served ${AUX_WAIT:-6}s after removal"; fi
done
verify_clean "aux sockets removed" no
verify_no_leak
