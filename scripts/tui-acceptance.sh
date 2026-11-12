#!/usr/bin/env bash
# Measures the terminal UI against the quality bar of design/tui.md: idle
# CPU with three running servers and the containers pane, and the CPU and
# the latency of `?` under a log flood of 10 000 lines per second and one
# of 2 000 lines per second of 1 000-character lines with wrapping on.
#
# Usage: scripts/tui-acceptance.sh [binary] [port] [work dir]
#
# It runs an isolated daemon on the port (default 7981) with its config
# and state in the work directory (default a new temporary one), and the
# terminal UI at 120x40 in a pty through scripts/tui-acceptance.py, which
# needs Python 3 and nothing else. Not run in CI: the numbers depend on
# the machine. The daemon and the UI are stopped at the end.
set -euo pipefail

bin=${1:-bin/portside}
port=${2:-7981}
work=${3:-$(mktemp -d "${TMPDIR:-/tmp}/tui-acceptance.XXXXXX")}
here=$(cd "$(dirname "$0")" && pwd)
python=${PYTHON:-python3}
idle_seconds=${IDLE_SECONDS:-30}
flood_seconds=${FLOOD_SECONDS:-20}

bin=$(cd "$(dirname "$bin")" && pwd)/$(basename "$bin")
[ -x "$bin" ] || { echo "no binary at $bin (make build)" >&2; exit 2; }
if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
	echo "port $port is taken" >&2
	exit 2
fi

mkdir -p "$work/projects/shop/web" "$work/projects/shop/api" "$work/projects/blog" "$work/projects/tools" "$work/state"
# 10 000 lines per second: batches of 1 000 timestamped lines every 0.1 s,
# accounting for the time the writes take; the wide flood writes 2 000
# lines of 1 000 characters per second the same way.
cat >"$work/flood.py" <<'EOF'
import sys, time
rate, width = int(sys.argv[1]), int(sys.argv[2])
batch = rate // 10
pad = "x" * max(0, width - 40)
n = 0
out = sys.stdout
next_at = time.monotonic()
while True:
    stamp = time.strftime("%H:%M:%S")
    out.write("".join(f"{stamp} INFO flood line {n + i:09d} {pad}\n" for i in range(batch)))
    out.flush()
    n += batch
    next_at += 0.1
    time.sleep(max(0.0, next_at - time.monotonic()))
EOF
cat >"$work/portside.toml" <<EOF
root = "$work/projects"
port = $port
restore = false
notify = false

[servers.web]
dir = "shop/web"
cmd = "python3 -m http.server {PORT} --bind 127.0.0.1"
port = 0
group = "shop"

[servers.api]
dir = "shop/api"
cmd = "python3 -m http.server {PORT} --bind 127.0.0.1"
port = 0
group = "shop"

[servers.blog]
dir = "blog"
cmd = "python3 -m http.server {PORT} --bind 127.0.0.1"
port = 0

[servers.flood]
dir = "tools"
cmd = "python3 -u $work/flood.py 10000 40"
port = 0

[servers.wide]
dir = "tools"
cmd = "python3 -u $work/flood.py 2000 1000"
port = 0
EOF

export PORTSIDE_CONFIG="$work/portside.toml" PORTSIDE_STATE_DIR="$work/state"
"$bin" daemon >"$work/daemon.log" 2>&1 &
daemon=$!
cleanup() {
	kill "$daemon" 2>/dev/null || true
	wait "$daemon" 2>/dev/null || true
}
trap cleanup EXIT

for _ in $(seq 100); do
	[ -s "$work/state/auth-token" ] && curl -fsS "http://127.0.0.1:$port/api/health" >/dev/null 2>&1 && break
	sleep 0.1
done

api() {
	curl -fsS -o /dev/null -H "Authorization: Bearer $(cat "$work/state/auth-token")" -X POST "http://127.0.0.1:$port/api/servers/$1/$2" -d '{}'
}

for s in web api blog; do api "$s" start; done
sleep 3

echo "machine: $(uname -sm), $( (sysctl -n machdep.cpu.brand_string 2>/dev/null || grep -m1 'model name' /proc/cpuinfo 2>/dev/null | cut -d: -f2) | sed 's/^ *//')"
echo "binary: $bin ($("$bin" --version 2>/dev/null || echo unknown version))"
echo "work: $work"

"$python" -I "$here/tui-acceptance.py" idle "$bin" "$idle_seconds"

api flood start
sleep 2
"$python" -I "$here/tui-acceptance.py" flood "$bin" "$flood_seconds" flood
api flood stop

api wide start
sleep 2
"$python" -I "$here/tui-acceptance.py" wide "$bin" "$flood_seconds" wide
api wide stop
