# shellcheck shell=bash
# lib.sh: shared guardrails for the hive daemon chaos harness.
#
# Every test runs against a throwaway daemon in its own HIVE_HOME under
# $CHAOS_ROOT. Nothing here may signal a process it cannot prove belongs to
# that sandbox (/proc/<pid>/environ must carry HIVE_HOME=<sandbox>), and the
# sandbox is torn down on every exit path. Hard thresholds abort a test and
# tear down before the machine is put at risk.

set -Eeuo pipefail

# ---------------------------------------------------------------- settings
CHAOS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$CHAOS_DIR/../.." && pwd)"
: "${CHAOS_ROOT:=${TMPDIR:-/tmp}/hive-chaos}"   # all sandboxes and the built binary live here
: "${CHAOS_RECORDS:=$CHAOS_ROOT/records}"      # one JSON + one Markdown record per test
: "${HIVE_BIN:=}"                               # empty: built from this checkout
: "${AGENTS:=4}"                                # tick agents in the fixture

# Hard thresholds. Crossing any of them aborts the test and tears down.
: "${MAX_LOAD_PER_CPU:=2.0}"     # 1-min load average per CPU, checked before and during a test
: "${MIN_MEM_AVAIL_MB:=1024}"    # MemAvailable floor
: "${MIN_DISK_FREE_MB:=1024}"    # free space floor on $CHAOS_ROOT
: "${MAX_SANDBOX_PROCS:=64}"     # processes owned by the sandbox
: "${MAX_DAEMON_RSS_MB:=512}"    # daemon resident memory
: "${MAX_DAEMON_FDS:=512}"       # daemon open files
: "${MAX_GOROUTINES:=2000}"      # daemon goroutines
: "${MAX_LOG_MB:=64}"            # sandbox logs directory
: "${RECOVERY_TIMEOUT:=30}"      # seconds for the daemon to serve again after a fault
: "${TEST_TIMEOUT:=300}"         # wall clock for one test
: "${LEAK_GOROUTINES:=10}"       # allowed goroutine growth across a test
: "${LEAK_FDS:=5}"               # allowed open-file growth across a test

SB=""            # this test's HIVE_HOME
TEST_NAME=""
TEST_START=0
declare -a CHECKS=()   # "PASS|FAIL|INFO<TAB>name<TAB>detail"
declare -A METRICS=()
FAILED=0
ABORTED=""
SKIPPED=""

# ------------------------------------------------------------------ output
log()  { printf '[chaos %s] %s\n' "$(date +%H:%M:%S)" "$*" >&2; }
die()  { log "FATAL: $*"; ABORTED="$*"; exit 3; }
# skip REASON: this test cannot run here (not a failure); recorded as SKIPPED.
skip() { log "SKIP: $*"; SKIPPED="$*"; CHECKS+=("INFO"$'\t'"skipped"$'\t'"$*"); exit 0; }

check() { # check PASS|FAIL|INFO name detail
	CHECKS+=("$1"$'\t'"$2"$'\t'"${3:-}")
	[[ $1 == FAIL ]] && FAILED=1
	log "$1  $2${3:+ ($3)}"
	return 0
}
metric() { METRICS[${1//[^A-Za-z0-9_]/_}]=$2; } # keys are safe words: the record splits on spaces

# ------------------------------------------------------------ hive wrapper
hive() {
	HIVE_HOME="$SB" HIVE_CONFIG="$SB/config.toml" HIVE_LOG=debug \
		timeout 20 "$HIVE_BIN" "$@"
}
hive_ok() { hive "$@" >/dev/null 2>&1; }

build_hive() {
	if [[ -n $HIVE_BIN ]]; then
		[[ -x $HIVE_BIN ]] || die "HIVE_BIN=$HIVE_BIN is not executable"
		return
	fi
	HIVE_BIN="$CHAOS_ROOT/bin/hive"
	if [[ ! -x $HIVE_BIN || -n ${CHAOS_REBUILD:-} ]]; then
		log "building hive into $HIVE_BIN"
		mkdir -p "$CHAOS_ROOT/bin"
		(cd "$REPO_ROOT" && go build -o "$HIVE_BIN" ./cmd/hive)
	fi
}

# ------------------------------------------------------- host-plane guards
# owned PID: alive, not us, not init, and launched for this sandbox.
owned() {
	local pid=$1
	[[ $pid =~ ^[0-9]+$ ]] && ((pid > 1 && pid != $$ && pid != BASHPID)) || return 1
	[[ -r /proc/$pid/environ ]] || return 1
	# Not "| grep -q": it exits at the first match, tr dies of SIGPIPE, and
	# pipefail turns the match into a failure (with a large environment).
	local env
	env=$({ tr '\0' '\n' <"/proc/$pid/environ"; } 2>/dev/null) || return 1
	grep -qxF "HIVE_HOME=$SB" <<<"$env"
}

# The only way this harness sends a signal.
safe_kill() { # safe_kill SIG PID
	local sig=$1 pid=$2
	owned "$pid" || die "refusing to send SIG$sig to PID $pid: not owned by sandbox $SB"
	kill -s "$sig" "$pid"
}

# safe_prlimit PID ARGS...: change a resource limit of a sandbox process only.
safe_prlimit() {
	local pid=$1; shift
	command -v prlimit >/dev/null || skip "no prlimit command"
	owned "$pid" || die "refusing to change limits of PID $pid: not owned by sandbox $SB"
	prlimit --pid "$pid" "$@"
}

sandbox_pids() {
	local d pid
	for d in /proc/[0-9]*; do
		pid=${d#/proc/}
		owned "$pid" && echo "$pid"
	done
	return 0
}

alive() { kill -0 "$1" 2>/dev/null; }

guard_sandbox_path() {
	[[ -n $SB ]] || die "sandbox path is empty"
	[[ $SB == "$CHAOS_ROOT"/sb-* ]] || die "sandbox $SB is not under $CHAOS_ROOT"
	[[ $SB != "${HOME:-/nonexistent}/.hive"* ]] || die "sandbox points at the real ~/.hive"
	# Unix socket path limit (104 on macOS, 108 on Linux).
	((${#SB} + 10 < 104)) || die "sandbox path too long for a unix socket: $SB (set CHAOS_ROOT shorter)"
}

load_per_cpu() { awk -v n="$(nproc)" '{printf "%.2f", $1/n}' /proc/loadavg; }
mem_avail_mb() { awk '/MemAvailable/{print int($2/1024)}' /proc/meminfo; }
disk_free_mb() { df -Pm "$CHAOS_ROOT" | awk 'NR==2{print $4}'; }

host_headroom() { # host_headroom phase -> dies when the host is short
	local l m d
	l=$(load_per_cpu) m=$(mem_avail_mb) d=$(disk_free_mb)
	awk -v l="$l" -v max="$MAX_LOAD_PER_CPU" 'BEGIN{exit !(l <= max)}' ||
		die "$1: load $l per CPU > $MAX_LOAD_PER_CPU"
	((m >= MIN_MEM_AVAIL_MB)) || die "$1: only ${m}MB memory available (< $MIN_MEM_AVAIL_MB)"
	((d >= MIN_DISK_FREE_MB)) || die "$1: only ${d}MB disk free (< $MIN_DISK_FREE_MB)"
}

# Checked after every fault and every few seconds while waiting.
watchdog() {
	local n pid rss fds logmb
	n=$(sandbox_pids | wc -l)
	((n <= MAX_SANDBOX_PROCS)) || die "sandbox runs $n processes (> $MAX_SANDBOX_PROCS)"
	pid=$(daemon_pid)
	if [[ -n $pid ]] && owned "$pid"; then
		rss=$(awk '/VmRSS/{print int($2/1024)}' "/proc/$pid/status" 2>/dev/null || echo 0)
		fds=$(find "/proc/$pid/fd" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l)
		((rss <= MAX_DAEMON_RSS_MB)) || die "daemon RSS ${rss}MB (> $MAX_DAEMON_RSS_MB)"
		((fds <= MAX_DAEMON_FDS)) || die "daemon has $fds open files (> $MAX_DAEMON_FDS)"
	fi
	logmb=$(du -sm "$SB/logs" 2>/dev/null | cut -f1 || echo 0)
	((logmb <= MAX_LOG_MB)) || die "sandbox logs ${logmb}MB (> $MAX_LOG_MB)"
	host_headroom "during $TEST_NAME"
	(($(date +%s) - TEST_START <= TEST_TIMEOUT)) || die "test ran past $TEST_TIMEOUT s"
}

# --------------------------------------------------------------- sandbox
daemon_pid() { cat "$SB/hive.pid" 2>/dev/null | tr -d '[:space:]' || true; }

# Shim and agent PIDs of running processes, from the shims' own records.
run_records() { # prints "procid shim_pid agent_pid status"
	local f
	for f in "$SB"/run/*/state.json; do
		[[ -f $f ]] || continue
		jq -r --arg id "$(basename "$(dirname "$f")")" \
			'"\($id) \(.shim_pid // 0) \(.pid // 0) \(.status // "?")"' "$f" 2>/dev/null || true
	done
}

teardown() {
	local rc=$? pids
	trap - EXIT INT TERM
	[[ -z $SB || ! -d $SB ]] && return
	log "teardown: stopping sandbox daemon"
	hive stop >/dev/null 2>&1 || true
	# Anything the stop left behind, owned PIDs only (frozen ones resumed first).
	pids=$(sandbox_pids)
	for p in $pids; do kill -s CONT "$p" 2>/dev/null || true; done
	sleep 0.5
	# Repeat until nothing of the sandbox is left: a straggling client may
	# autostart a daemon while we kill. Not safe_kill: a short-lived child may
	# exit between listing and signal, which must not abort the teardown.
	local round all=""
	for round in 1 2 3 4 5 6 7 8 9 10; do
		pids=$(sandbox_pids)
		[[ -z $pids ]] && break
		all+="$(echo $pids) "
		log "teardown: SIGKILL leftovers (pass $round): $(echo $pids)"
		for p in $pids; do { owned "$p" && kill -s KILL "$p"; } 2>/dev/null || true; done
		sleep 0.3
	done
	[[ -n $all ]] && CHECKS+=("INFO"$'\t'"teardown leftovers"$'\t'"$all")
	pids=$(sandbox_pids)
	if [[ -n $pids ]]; then
		ABORTED="teardown could not clear sandbox processes: $(echo $pids)"
		log "FATAL: $ABORTED"
	fi
	[[ -z $(sandbox_pids) ]] && clean_hashed_sockets
	if [[ -n ${SB_MOUNTED:-} && -z $(sandbox_pids) ]]; then
		umount -- "$SB" 2>/dev/null || umount -l -- "$SB" 2>/dev/null || log "could not unmount $SB"
		touch "$SB/.chaos-sandbox"
	fi
	write_record "$rc"
	if [[ -f $SB/.chaos-sandbox && -z ${CHAOS_KEEP:-} && -z $(sandbox_pids) ]]; then
		rm -rf -- "$SB"
	else
		log "sandbox kept at $SB"
	fi
	if [[ -n $ABORTED ]]; then exit 3; fi
	exit $((FAILED ? 1 : rc))
}

# begin NAME: build, preflight, make the sandbox, start the fixture.
begin() {
	TEST_NAME=$1
	TEST_START=$(date +%s)
	command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }
	mkdir -p "$CHAOS_ROOT" "$CHAOS_RECORDS"
	build_hive
	host_headroom "preflight"
	SB=$(mktemp -d "$CHAOS_ROOT/sb-XXXXXX")
	guard_sandbox_path
	touch "$SB/.chaos-sandbox"
	trap teardown EXIT
	trap 'die "interrupted"' INT TERM
	metric host_load_per_cpu_before "$(load_per_cpu)"
	metric host_mem_avail_mb_before "$(mem_avail_mb)"
	log "test $TEST_NAME in sandbox $SB"
}

# mount_sandbox_tmpfs SIZE: put the sandbox on a size-capped tmpfs, so a
# disk-full test can only ever fill that, never the real disk.
mount_sandbox_tmpfs() {
	[[ $(id -u) == 0 ]] || skip "a size-capped tmpfs sandbox needs root"
	command -v mount >/dev/null || skip "no mount command"
	mountpoint -q "$SB" && die "$SB is already a mountpoint"
	mount -t tmpfs -o "size=$1,mode=0700" tmpfs "$SB" 2>/dev/null || skip "cannot mount a tmpfs here (container without the capability?)"
	SB_MOUNTED=1
	touch "$SB/.chaos-sandbox"
}

# sock_path DIR NAME: where hive puts socket NAME for DIR (platform.SocketPath):
# DIR/NAME when it fits in 100 bytes, else a hashed name in /tmp/hive-<uid>.
sock_path() {
	python3 - "$1" "$2" <<'PY'
import hashlib, os, sys
p = os.path.join(sys.argv[1], sys.argv[2])
if len(p) > 100:
    p = "/tmp/hive-%d/%s.sock" % (os.getuid(), hashlib.sha256(os.path.abspath(p).encode()).hexdigest()[:24])
print(p)
PY
}

# Hashed sockets of this sandbox (daemon's and shims') that nothing serves:
# hive cannot clean those once the sandbox is gone.
clean_hashed_sockets() {
	local d n sp
	for d in "$SB" "$SB"/run/*/; do
		d=${d%/}
		for n in hive.sock member.sock observer.sock shim.sock; do
			sp=$(sock_path "$d" "$n")
			[[ $sp == /tmp/hive-"$(id -u)"/* && -S $sp ]] || continue
			served "$sp" || rm -f -- "$sp"
		done
	done
}

# served SOCKET: something accepts connections on it.
served() {
	python3 -c 'import socket,sys; s=socket.socket(socket.AF_UNIX); s.settimeout(2); s.connect(sys.argv[1])' "$1" 2>/dev/null
}

# fixture: one environment, $AGENTS PTY agents printing "tick N".
TICK='i=0; while :; do i=$((i+1)); echo "tick $i"; sleep 0.2; done'
fixture() {
	local pane
	hive_ok env create dev --managed || die "fixture: env create failed"
	local per=${PANES_PER_TAB:-4}
	for ((i = 0; i < AGENTS; i++)); do
		if ((i % per == 0)); then
			pane=$(hive --json tab create dev -- sh -c "$TICK" | jq -r .pane.id) || die "fixture: tab create failed"
		else
			hive_ok pane split "$pane" --no-focus -- sh -c "$TICK" || die "fixture: pane split failed"
		fi
	done
	wait_serving "fixture" || die "fixture never served"
	sleep 1
	metric agents "$AGENTS"
	BASE_STATS=$(hive --json debug stats)
	BASE_PID=$(daemon_pid)
	LOG_MARK=$(wc -l <"$SB/logs/daemon.log" 2>/dev/null || echo 0)
}

# ----------------------------------------------------------------- probes
wait_serving() { # wait_serving what -> sets RECOVERY_MS
	local t0 now
	t0=$(date +%s%N)
	while :; do
		if hive_ok pane list; then
			now=$(date +%s%N)
			RECOVERY_MS=$(((now - t0) / 1000000))
			return 0
		fi
		(($(date +%s%N) - t0 > RECOVERY_TIMEOUT * 1000000000)) && return 1
		watchdog
		sleep 0.1
	done
}

wait_dead() { # wait_dead pid [seconds]
	local i
	for ((i = 0; i < ${2:-10} * 10; i++)); do alive "$1" || return 0; sleep 0.1; done
	return 1
}

last_tick() { # last_tick process_id -> highest tick on the screen
	{ hive terminal snapshot "$1" 2>/dev/null | grep -oE 'tick [0-9]+' | awk '{print $2}' | sort -n | tail -1; } || true
}

# verify_clean LABEL EXPECT_NEW_PID(yes|no|any) [EXPECT_LIVE_AGENTS]
# The standard "daemon came back and is clean" verdict.
verify_clean() {
	local label=$1 newpid=$2 want_live=${3:-$AGENTS}
	local pid panes stuck live ticking a b id st newlog orphans

	if wait_serving "$label"; then
		check PASS "serving after $label" "${RECOVERY_MS}ms"
		metric recovery_ms "$RECOVERY_MS"
	else
		check FAIL "serving after $label" "not within ${RECOVERY_TIMEOUT}s"
		return
	fi
	watchdog

	pid=$(daemon_pid)
	metric daemon_pid_before "$BASE_PID"; metric daemon_pid_after "$pid"
	if ! owned "$pid"; then check FAIL "daemon pid" "pidfile $pid is not a sandbox process"
	elif [[ $newpid == yes && $pid == "$BASE_PID" ]]; then check FAIL "daemon restarted" "pid unchanged ($pid)"
	elif [[ $newpid == no && $pid != "$BASE_PID" ]]; then check FAIL "same daemon" "pid $BASE_PID -> $pid"
	else check PASS "daemon pid" "$BASE_PID -> $pid"; fi

	# Exactly one daemon in the sandbox (one retry: a process mid-exit or
	# mid-exec reads empty in /proc for a moment).
	local daemons=0 p try
	for try in 1 2; do
		daemons=0
		for p in $(sandbox_pids); do
			grep -qE '^[^ ]+ daemon( |$)' <<<"$({ tr '\0' ' ' <"/proc/$p/cmdline"; } 2>/dev/null)" && daemons=$((daemons + 1))
		done
		((daemons == 1)) && break
		sleep 0.5
	done
	((daemons == 1)) && check PASS "single daemon" "" || check FAIL "single daemon" "$daemons running"

	panes=$(hive --json pane list)
	local n want_panes=${EXPECT_PANES:-$AGENTS}; n=$(jq length <<<"$panes")
	if [[ $want_panes == any ]]; then check INFO "panes" "$n"
	elif ((n == want_panes)); then check PASS "panes kept" "$n/$want_panes"
	else check FAIL "panes kept" "$n/$want_panes"; fi
	stuck=$(jq '[.[] | select(.process.status == "starting")] | length' <<<"$panes")
	((stuck == 0)) && check PASS "no pane stuck starting" "" || check FAIL "no pane stuck starting" "$stuck stuck"

	# Live agents keep ticking: the counter on screen advances.
	live=0 ticking=0
	for id in $(jq -r '.[] | select(.process.status == "running") | .process_id' <<<"$panes"); do
		live=$((live + 1))
		a=$(last_tick "$id"); sleep 0.6; b=$(last_tick "$id")
		[[ -n $a && -n $b && $b -gt $a ]] && ticking=$((ticking + 1))
	done
	metric agents_running "$live"; metric agents_ticking "$ticking"
	if [[ $want_live == any ]]; then check INFO "agents running" "$live"
	elif ((live == want_live)); then check PASS "agents running" "$live/$want_live"
	else check FAIL "agents running" "$live, want $want_live"; fi
	((ticking == live)) && check PASS "agents still ticking" "$ticking/$live" || check FAIL "agents still ticking" "$ticking/$live"

	# Every running record's shim and agent are alive and ours; nothing else is.
	# (Cross-checked with the daemon: a SIGKILLed shim never rewrites its file.)
	local running_ids stale=0
	running_ids=$(jq -r '.[] | select(.process.status == "running") | .process_id' <<<"$panes")
	while read -r id shim agent st; do
		if ! grep -qxF "$id" <<<"$running_ids"; then stale=$((stale + 1)); continue; fi
		if ! owned "$shim"; then
			sleep 0.5 # an exiting shim may still be its agent's parent for a moment
			owned "$shim" || check FAIL "shim alive" "$id: shim $shim $(grep -h '^State' "/proc/$shim/status" 2>/dev/null || echo gone); agent $agent parent now $(awk '{sub(/^.*\) /,""); print $2}' "/proc/$agent/stat" 2>/dev/null || echo gone); log: $(grep -F "$id" "$SB/logs/daemon.log" | sed -E 's/^time=[^ ]+ //' | cut -c1-140 | tr '\n' '|')"
		fi
		if ! owned "$agent"; then
			sleep 0.5
			local dpid dst
			dpid=$(hive --json pane list | jq -r --arg id "$id" '.[]|select(.process_id==$id)|.process.pid')
			dst=$(hive --json pane list | jq -r --arg id "$id" '.[]|select(.process_id==$id)|.process.status')
			owned "$agent" || check FAIL "agent alive" "$id: state.json agent $agent $(grep -h '^State' "/proc/$agent/status" 2>/dev/null || echo gone); daemon now says ${dst:-<no pane>} pid ${dpid:-?} ($(owned "${dpid:-0}" && echo alive || echo dead)); log: $(grep -F "$id" "$SB/logs/daemon.log" | sed -E 's/^time=[^ ]+ //' | cut -c1-140 | tr '\n' '|')"
		fi
	done < <(run_records)
	metric stale_shim_dirs "$stale"
	((stale == 0)) || check INFO "stale shim dirs" "$stale run/ dirs for agents the daemon no longer runs"
	# Orphan: a sandbox process that is not the daemon, a live shim, or in a
	# live agent's process group (agents lead their groups; their children
	# such as "sleep" share it).
	local -A allowed_pid=() allowed_grp=()
	allowed_pid[$pid]=1
	# The daemon's view of each live agent's PID, its parent being the shim;
	# a shim's own state.json may be torn (test 09), so it is not trusted.
	local apid
	for apid in $(jq -r '.[] | select(.process.status == "running") | .process.pid' <<<"$panes"); do
		allowed_grp[$apid]=1
		allowed_pid[$(awk '{sub(/^.*\) /,""); print $2}' "/proc/$apid/stat" 2>/dev/null || echo 0)]=1
	done
	local orphan_list="" pgid
	for p in $(sandbox_pids); do
		[[ -n ${allowed_pid[$p]:-} ]] && continue
		pgid=$(awk '{sub(/^.*\) /,""); print $3}' "/proc/$p/stat" 2>/dev/null) || continue
		[[ -n $pgid && -n ${allowed_grp[$pgid]:-} ]] && continue
		alive "$p" && orphan_list+="$p($({ tr '\0' ' ' <"/proc/$p/cmdline"; } 2>/dev/null | cut -c1-40)) "
	done
	metric orphans "${orphan_list:-none}"
	# For an orphaned shim, what the daemon logged about its agent.
	local oid
	for oid in $(grep -oE '__shim [^ )]*' <<<"$(for p in $(sandbox_pids); do { tr '\0' ' ' <"/proc/$p/cmdline"; } 2>/dev/null; echo; done)" | awk '{print $2}' | xargs -rn1 basename); do
		grep -qxF "$oid" <<<"$running_ids" && continue
		check INFO "untracked shim $oid" "$(grep -F "$oid" "$SB/logs/daemon.log" 2>/dev/null | sed -E 's/^time=[^ ]+ //' | cut -c1-200 | tr '\n' '|' )"
	done
	[[ -z $orphan_list ]] && check PASS "no orphans" "daemon + $live shims + $live agent groups only" ||
		check FAIL "no orphans" "$orphan_list"

	# The daemon that served at the start of these checks still does: a
	# different one means it died while being checked.
	local endpid; endpid=$(daemon_pid)
	if [[ $endpid != "$pid" ]]; then
		check FAIL "daemon stable during checks" "$pid -> $endpid; log: $(tail -n 5 "$SB/logs/daemon.log" | sed -E 's/^time=[^ ]+ //' | cut -c1-140 | tr '\n' '|')"
	fi

	# No panics or fatal errors in the daemon log since the fixture.
	newlog=$(tail -n +"$((LOG_MARK + 1))" "$SB/logs/daemon.log" 2>/dev/null || true)
	if grep -qiE 'panic|fatal error|data race' <<<"$newlog"; then
		check FAIL "log clean" "$(grep -iE 'panic|fatal error|data race' <<<"$newlog" | head -1 | cut -c1-160)"
	else
		check PASS "log clean" "$(grep -ciE '"level":"error"|level=error' <<<"$newlog" || true) error lines"
	fi
}

# Leak check against the fixture's stats, after the same light load.
verify_no_leak() {
	local s0 s1 g0 g1 f0 f1
	for _ in $(seq 20); do hive_ok pane list; done
	sleep 1; s0=$(hive --json debug stats)
	for _ in $(seq 20); do hive_ok pane list; done
	sleep 1; s1=$(hive --json debug stats)
	g0=$(jq .goroutines <<<"$s0") g1=$(jq .goroutines <<<"$s1")
	f0=$(jq .open_fds <<<"$s0") f1=$(jq .open_fds <<<"$s1")
	metric goroutines "$g0 -> $g1"; metric open_fds "$f0 -> $f1"
	metric heap_mb "$(jq '.heap_alloc/1048576|floor' <<<"$s1")"
	((g1 <= MAX_GOROUTINES)) || die "daemon goroutines $g1 > $MAX_GOROUTINES"
	if ((g1 <= g0 + LEAK_GOROUTINES && f1 <= f0 + LEAK_FDS)); then
		check PASS "no leak under load" "goroutines $g0->$g1, fds $f0->$f1"
	else
		check FAIL "no leak under load" "goroutines $g0->$g1, fds $f0->$f1"
	fi
}

# ----------------------------------------------------------------- record
write_record() {
	local rc=$1 ts base verdict k checks_json metrics_json
	[[ -n $TEST_NAME ]] || return 0
	ts=$(date -u +%Y%m%dT%H%M%SZ)
	base="$CHAOS_RECORDS/$ts-$TEST_NAME"
	if [[ -n $ABORTED ]]; then verdict=ABORTED
	elif [[ -n $SKIPPED ]]; then verdict=SKIPPED
	elif ((FAILED || rc != 0)); then verdict=FAIL
	else verdict=PASS; fi
	checks_json=$(for c in "${CHECKS[@]}"; do
		IFS=$'\t' read -r r n d <<<"$c"
		jq -nc --arg r "$r" --arg n "$n" --arg d "$d" '{result:$r,check:$n,detail:$d}'
	done | jq -s .)
	metrics_json=$(for k in "${!METRICS[@]}"; do jq -nc --arg k "$k" --arg v "${METRICS[$k]}" '{($k):$v}'; done | jq -s 'add // {}')
	jq -n --arg test "$TEST_NAME" --arg verdict "$verdict" --arg aborted "$ABORTED" \
		--arg started "$(date -u -d "@$TEST_START" +%FT%TZ)" --argjson secs "$(($(date +%s) - TEST_START))" \
		--arg bin "$HIVE_BIN" --arg version "$("$HIVE_BIN" version 2>/dev/null | head -1)" \
		--argjson checks "$checks_json" --argjson metrics "$metrics_json" \
		'{test:$test,verdict:$verdict,aborted:(if $aborted == "" then null else $aborted end),started:$started,seconds:$secs,
		  hive:{bin:$bin,version:$version},metrics:$metrics,checks:$checks}' >"$base.json"
	# A record that does not parse is worse than none: say so.
	jq -e '.test' "$base.json" >/dev/null 2>&1 || log "WARNING: JSON record $base.json is empty or invalid"
	{
		echo "## $TEST_NAME: $verdict"
		echo
		[[ -n $ABORTED ]] && { echo "**Aborted by guardrail:** $ABORTED"; echo; }
		echo "| Result | Check | Detail |"; echo "|---|---|---|"
		for c in "${CHECKS[@]}"; do IFS=$'\t' read -r r n d <<<"$c"; echo "| $r | $n | $d |"; done
		echo; echo "| Metric | Value |"; echo "|---|---|"
		for k in $(printf '%s\n' "${!METRICS[@]}" | sort); do echo "| $k | ${METRICS[$k]} |"; done
	} >"$base.md"
	echo
	cat "$base.md"
	echo
	log "record: $base.json"
}
