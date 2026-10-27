#!/usr/bin/env bash
# Measures what an idle daemon costs: an isolated daemon (config and state in
# a fresh temporary directory, a free port, never the account's own daemon)
# with N trivial servers (`python3 -m http.server`), M of them running. A
# wrapper put first on the daemon's PATH counts its `ps` and `lsof` calls.
# For each condition the script reports the daemon's CPU time and those
# calls over a fixed window:
#
#   - no events client;
#   - one events client with usage=0 (the menu bar app with its panel closed);
#   - one events client showing usage (an open dashboard).
#
# With --watch F, the first running server watches F files (`watch =
# ["**/*.py"]` over a tree of F Python files in its directory).
#
# The events clients are scripts/benchclient. Needs python3, and go unless
# --binary is given (the benchclient is always built). macOS and Linux.
#
#   scripts/bench-idle.sh [--binary PATH] [--servers N] [--running M] [--window SECONDS] [--watch FILES]
set -euo pipefail
cd "$(dirname "$0")/.."

binary="" servers=21 running=5 window=60 settle=10 watch=0
while [ $# -gt 0 ]; do
  case "$1" in
    --binary) binary="$2"; shift 2 ;;
    --servers) servers="$2"; shift 2 ;;
    --running) running="$2"; shift 2 ;;
    --window) window="$2"; shift 2 ;;
    --watch) watch="$2"; shift 2 ;;
    -h|--help) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
case "$servers$running$window$watch" in *[!0-9]*) echo "--servers, --running, --window and --watch take whole numbers" >&2; exit 2 ;; esac
if [ "$watch" -gt 0 ] && [ "$running" -lt 1 ]; then echo "--watch needs a running server" >&2; exit 2; fi
if [ "$running" -gt "$servers" ] || [ "$servers" -lt 1 ] || [ "$window" -lt 1 ]; then
  echo "need 1 <= servers, running <= servers and window >= 1" >&2
  exit 2
fi
command -v python3 >/dev/null || { echo "python3 is required" >&2; exit 1; }

# Nothing from the caller's environment may point the daemon or the CLI
# elsewhere, such as at the account's own daemon.
for v in $(env | sed -n 's/^\(PORTSIDE_[A-Za-z0-9_]*\)=.*/\1/p'); do unset "$v"; done

REAL_PS="$(command -v ps)"
REAL_LSOF="$(command -v lsof || true)"
T="$(mktemp -d "${TMPDIR:-/tmp}/portside-bench.XXXXXX")"
PS_LOG="$T/ps.log" LSOF_LOG="$T/lsof.log"
dpid="" cpid=""

cleanup() {
  status=$?
  set +e
  if [ -n "$cpid" ] && kill -0 "$cpid" 2>/dev/null; then kill "$cpid"; wait "$cpid" 2>/dev/null; fi
  if [ -n "$dpid" ] && kill -0 "$dpid" 2>/dev/null; then
    "$PS" daemon stop >/dev/null 2>&1
    for _ in $(seq 1 100); do kill -0 "$dpid" 2>/dev/null || break; sleep 0.1; done
    if kill -0 "$dpid" 2>/dev/null; then kill -TERM "$dpid"; fi
    wait "$dpid" 2>/dev/null
  fi
  if command -v trash >/dev/null 2>&1 && trash "$T"; then
    :
  else
    echo "left the benchmark directory in place: $T" >&2
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

if [ -n "$binary" ]; then
  PS="$(cd "$(dirname "$binary")" && pwd)/$(basename "$binary")"
  [ -x "$PS" ] || { echo "not an executable: $binary" >&2; exit 1; }
else
  PS="$T/portside"
  echo "building the binary..." >&2
  go build -o "$PS" ./cmd/portside
fi
go build -o "$T/benchclient" ./scripts/benchclient

# The ps and lsof wrappers: one line per call with the call's user and
# system CPU seconds and its arguments.
mkdir -p "$T/bin"
wrap() { # command name, real path, log
  cat > "$T/bin/$1" <<EOF
#!/bin/bash
args="\$*"
TIMEFORMAT="%3U %3S \${args//\\%/%%}"
exec 3>&2
{ time "$2" "\$@" 2>&3; } 2>>"$3"
EOF
  chmod +x "$T/bin/$1"
  : > "$3"
}
wrap ps "$REAL_PS" "$PS_LOG"
if [ -n "$REAL_LSOF" ]; then wrap lsof "$REAL_LSOF" "$LSOF_LOG"; else : > "$LSOF_LOG"; fi

# Free ports for the daemon and every server, held together so they differ.
read -r -a ports <<< "$(python3 - "$((servers + 1))" <<'PY'
import socket, sys
held = [socket.socket() for _ in range(int(sys.argv[1]))]
for s in held:
    s.bind(("127.0.0.1", 0))
print(" ".join(str(s.getsockname()[1]) for s in held))
PY
)"
port="${ports[0]}"
if [ "$port" = 7878 ]; then echo "refusing port 7878, the account's daemon port" >&2; exit 1; fi

export PORTSIDE_CONFIG="$T/portside.toml" PORTSIDE_STATE_DIR="$T/state"
{
  printf 'port = %s\nrestore = false\nnotify = false\n' "$port"
  for i in $(seq 1 "$servers"); do
    name="$(printf 's%02d' "$i")"
    mkdir -p "$T/projects/$name"
    printf '\n[servers.%s]\ndir = "%s"\ncmd = "python3 -m http.server {PORT} --bind 127.0.0.1"\nport = %s\n' "$name" "$T/projects/$name" "${ports[$i]}"
    if [ "$i" -le "$running" ]; then printf 'group = "running"\n'; fi
    if [ "$i" -eq 1 ] && [ "$watch" -gt 0 ]; then
      printf 'watch = ["**/*.py"]\n'
      # 50 files per directory, two levels deep.
      python3 - "$T/projects/$name" "$watch" <<'PY'
import os, sys
root, n = sys.argv[1], int(sys.argv[2])
for i in range(n):
    d = os.path.join(root, "pkg%02d" % (i // 500), "mod%02d" % (i // 50 % 10))
    os.makedirs(d, exist_ok=True)
    with open(os.path.join(d, "f%04d.py" % i), "w") as f:
        f.write("x = %d\n" % i)
PY
    fi
  done
} > "$PORTSIDE_CONFIG"

PATH="$T/bin:$PATH" "$PS" daemon > "$T/daemon.log" 2>&1 &
dpid=$!
for _ in $(seq 1 150); do
  "$PS" daemon info --no-start >/dev/null 2>&1 && break
  kill -0 "$dpid" 2>/dev/null || { cat "$T/daemon.log" >&2; exit 1; }
  sleep 0.2
done
"$PS" daemon info --no-start >/dev/null || { echo "the daemon did not answer" >&2; exit 1; }
if [ "$running" -gt 0 ]; then "$PS" up running --wait >/dev/null; fi
sleep "$settle" # the first login-shell read and the first samples

cpu_seconds() {
  if [ -r "/proc/$1/stat" ]; then
    awk -v hz="$(getconf CLK_TCK)" '{ sub(/^.*\) /, ""); printf "%.2f\n", ($12 + $13) / hz }' "/proc/$1/stat"
  else
    "$REAL_PS" -o time= -p "$1" | awk -F: '{ s = 0; for (i = 1; i <= NF; i++) s = s * 60 + $i; printf "%.2f\n", s }'
  fi
}

rows=()
measure() {
  local c0 c1 l0 l1 s0 s1 lsof
  c0="$(cpu_seconds "$dpid")"
  l0="$(wc -l < "$PS_LOG")" s0="$(wc -l < "$LSOF_LOG")"
  sleep "$window"
  c1="$(cpu_seconds "$dpid")"
  l1="$(wc -l < "$PS_LOG")" s1="$(wc -l < "$LSOF_LOG")"
  lsof="$(awk -v l0="$s0" -v l1="$s1" 'NR > l0 && NR <= l1 { calls++; t += $1 + $2 } END { printf "%10d %9.2f s", calls, t }' "$LSOF_LOG")"
  rows+=("$(awk -v l0="$l0" -v l1="$l1" -v label="$1" -v lsof="$lsof" -v cpu="$(awk -v a="$c0" -v b="$c1" 'BEGIN { print b - a }')" -v w="$window" '
      NR <= l0 || NR > l1 { next }
      { calls++; pscpu += $1 + $2; if ($3 == "-A" && $5 ~ /^pid=,ppid=,rss=/) usage++ }
      END { printf "%-26s %9.2f s %9.2f s %8d %9d %9.2f s %s\n", label, cpu, cpu * 60 / w, calls, usage, pscpu, lsof }' "$PS_LOG")")
}

with_client() {
  "$T/benchclient" -port "$port" -usage="$2" -for "$((window + settle + 30))s" 2>/dev/null &
  cpid=$!
  sleep "$settle"
  measure "$1"
  kill "$cpid" 2>/dev/null || true
  wait "$cpid" 2>/dev/null || true
  cpid=""
}

echo "measuring three windows of ${window} s..." >&2
measure "no events client"
with_client "client with usage=0" false
with_client "client showing usage" true

if [ "$(uname)" = Darwin ]; then
  machine="$(sysctl -n machdep.cpu.brand_string), macOS $(sw_vers -productVersion)"
else
  machine="$(sed -n 's/^model name[[:space:]]*: //p' /proc/cpuinfo | head -n 1), $(uname -sr)"
fi
containers="$(docker ps -q 2>/dev/null | wc -l | tr -d ' ' || true)"
echo
echo "$("$PS" version 2>/dev/null | head -n 1); $machine; $("$REAL_PS" -A | wc -l | tr -d ' ') processes; ${containers:-0} running containers"
echo "$running of $servers servers running; ${watch} watched files; window ${window} s"
echo
printf '%-26s %11s %11s %8s %9s %11s %10s %11s\n' condition "daemon CPU" "per minute" "ps calls" "usage ps" "ps CPU" "lsof calls" "lsof CPU"
printf '%s\n' "${rows[@]}"
