#!/usr/bin/env bash
# End-to-end check of the Linux login service on a machine with systemd and
# no desktop: install, restarts and crash commands, the journal, an in-place
# upgrade, restore after a service restart, a start that fails, and an
# isolated instance next to the service. It uses the default config and state
# locations, so run it as a normal user with sudo (for lingering) on a
# disposable machine, such as a CI runner.
#
# Usage: scripts/linux-service-e2e.sh <binary> <second binary of another version>
set -euo pipefail

bin=$(realpath "$1")
next=$(realpath "$2")
fail() { echo "FAIL: $*" >&2; exit 1; }
step() { echo "== $*"; }
# wait_for <seconds> <command...> retries the command five times a second.
wait_for() {
	local n=$1
	shift
	for _ in $(seq $((n * 5))); do
		if "$@" >/dev/null 2>&1; then return 0; fi
		sleep 0.2
	done
	return 1
}
field() { # field <server> <key>: a value from `status --json`
	portside status --json | python3 -c "import sys,json; print(next((s.get('$2','') for s in json.load(sys.stdin) if s['name']=='$1'),''))"
}
is() { [ "$(field "$1" status)" = "$2" ]; }
active_state() { systemctl --user show -p ActiveState --value portside.service; }
main_pid() { systemctl --user show -p MainPID --value portside.service; }
state_is() { [ "$(active_state)" = "$1" ]; }

[ -e ~/.config/portside ] || [ -e ~/.local/state/portside ] && fail "Portside config or state exists; run this on a disposable machine"

step "lingering and the user manager"
sudo loginctl enable-linger "$USER"
export XDG_RUNTIME_DIR="/run/user/$(id -u)"
wait_for 15 test -S "$XDG_RUNTIME_DIR/bus" || fail "no systemd user manager"
export DBUS_SESSION_BUS_ADDRESS="unix:path=$XDG_RUNTIME_DIR/bus"
mkdir -p ~/.local/bin
install -m 755 "$bin" ~/.local/bin/portside
export PATH="$HOME/.local/bin:$PATH"

work=$(mktemp -d)
mkdir -p "$work/apps" ~/.config/portside
cat > ~/.config/portside/portside.toml <<EOF
port = 7878
root = "$work/apps"
on_crash = 'printf "%s %s %s\n" "\$PORTSIDE_SERVER" "\$PORTSIDE_EXIT_CODE" "\$PORTSIDE_RESTARTS" >> $work/calls'

[servers.web]
cmd = "python3 -m http.server {PORT} --bind 127.0.0.1"
health = "/"
restart = "on-failure"

[servers.worker]
cmd = "while true; do sleep 1; done"
port = 0
restart = "on-failure"

[servers.failing]
cmd = "echo boom >&2; exit 2"
port = 0
restart = "on-failure"
EOF

step "service install"
portside service install | tee "$work/install"
grep -q "starts at boot" "$work/install" || fail "service install did not say it starts at boot"
wait_for 10 state_is active || fail "service not active: $(active_state)"
portside doctor > "$work/doctor" || true
grep -q "on_crash reports crashes of 3 of 3 servers" "$work/doctor" || { cat "$work/doctor"; fail "doctor did not count the crash commands"; }

step "servers, restarts and on_crash"
portside up web worker failing > /dev/null
wait_for 5 is worker running || fail "worker with port = 0 not running: $(field worker status)"
wait_for 15 is web running || fail "web not running: $(field web note)"
wait_for 50 grep -q "^failing 2 5$" "$work/calls" || fail "on_crash for failing: $(cat "$work/calls" 2>/dev/null)"
[ "$(field failing restarts)" = 5 ] || fail "failing restarts: $(field failing restarts)"
portside down web > /dev/null
sleep 1
[ "$(wc -l < "$work/calls")" = 1 ] || fail "on_crash ran more than once or for a stop: $(cat "$work/calls")"

step "daemon logs from the journal"
portside daemon logs -n 50 > "$work/logs"
grep -q "on_crash for failing ran" "$work/logs" || { cat "$work/logs"; fail "daemon logs does not show the journal"; }

step "in-place upgrade"
pid=$(main_pid)
install -m 755 "$next" ~/.local/bin/portside
portside daemon restart
[ "$(main_pid)" = "$pid" ] || fail "upgrade changed the pid: $pid -> $(main_pid)"
is worker running || fail "worker did not survive the upgrade"
portside daemon info | python3 -c "import sys,json; v=json.load(sys.stdin)['version']; sys.exit(0 if v=='$(portside version | cut -d' ' -f2)' else 'daemon runs '+v)"

step "restore after a service restart"
systemctl --user restart portside.service
wait_for 15 is worker running || fail "worker not restored after a service restart"

step "a start that fails is in the journal"
systemctl --user stop portside.service
python3 -m http.server 7878 --bind 127.0.0.1 > /dev/null 2>&1 &
holder=$!
wait_for 5 bash -c "exec 3<>/dev/tcp/127.0.0.1/7878" || fail "no listener holds 7878"
systemctl --user start portside.service || true
wait_for 30 state_is failed || fail "service did not fail with its port taken: $(active_state)"
portside daemon logs -n 30 > "$work/failed"
grep -q "7878" "$work/failed" || { cat "$work/failed"; fail "daemon logs does not show why the start failed"; }
kill "$holder"
systemctl --user reset-failed portside.service
systemctl --user start portside.service
wait_for 10 state_is active || fail "service did not start again: $(active_state)"

step "an isolated instance reads its own log file"
printf 'port = 7931\nroot = "%s"\n' "$work/apps" > "$work/iso.toml"
PORTSIDE_CONFIG="$work/iso.toml" PORTSIDE_STATE_DIR="$work/iso-state" portside daemon start > /dev/null
PORTSIDE_CONFIG="$work/iso.toml" PORTSIDE_STATE_DIR="$work/iso-state" portside daemon logs -n 20 > "$work/iso-logs"
grep -q "7931" "$work/iso-logs" || { cat "$work/iso-logs"; fail "the isolated instance's logs are not its own"; }
grep -q "on_crash for failing ran" "$work/iso-logs" && fail "the isolated instance showed the service's journal"
PORTSIDE_CONFIG="$work/iso.toml" PORTSIDE_STATE_DIR="$work/iso-state" portside daemon stop > /dev/null 2> "$work/iso-stop"
grep -q "login service" "$work/iso-stop" && fail "stopping the isolated instance mentioned the service: $(cat "$work/iso-stop")"

step "service uninstall"
portside service uninstall
[ "$(active_state)" = inactive ] || fail "service still $(active_state)"
echo "all passed"
