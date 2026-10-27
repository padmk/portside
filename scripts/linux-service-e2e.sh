#!/usr/bin/env bash
# End-to-end check of the Linux login service on a machine with systemd and
# no desktop: install, readiness by a log line, restarts and crash commands,
# `check`, the journal, an in-place upgrade, restore after a service
# restart, a start that fails, and an isolated instance next to the service. It uses the default config and state
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
cmd = "sleep 2; echo Worker ready; while true; do sleep 1; done"
port = 0
ready_log = "Worker ready"
restart = "on-failure"

[servers.slow]
cmd = "sleep 4; echo Slow ready; while true; do sleep 1; done"
port = 0
ready_log = "^Slow ready$"

[servers.failing]
cmd = "echo boom >&2; exit 2"
port = 0
restart = "on-failure"

# Stops answering after 4 s and answers again 10 s later.
[servers.flaky]
cmd = "python3 -m http.server {PORT} --bind 127.0.0.1 & p=\$!; sleep 4; kill \$p; sleep 10; exec python3 -m http.server {PORT} --bind 127.0.0.1"
unready_after = "2s"
on_unready = 'echo "\$PORTSIDE_EVENT" >> $work/events'
on_recover = 'echo "\$PORTSIDE_EVENT \$PORTSIDE_RECOVERED_FROM" >> $work/events'

# Its start fails (no directory), so retry_after starts it again.
[servers.retrying]
cmd = "true"
dir = "$work/missing"
port = 0
restart = "on-failure"
retry_after = "3s"
on_crash = 'echo "\$PORTSIDE_TRIGGER \$PORTSIDE_STREAK" >> $work/retries'
EOF

step "service install"
portside service install | tee "$work/install"
grep -q "starts at boot" "$work/install" || fail "service install did not say it starts at boot"
wait_for 10 state_is active || fail "service not active: $(active_state)"
portside doctor > "$work/doctor" || true
grep -q "on_crash reports crashes of 6 of 6 servers" "$work/doctor" || { cat "$work/doctor"; fail "doctor did not count the crash commands"; }

step "servers, restarts and on_crash"
portside up web worker failing > /dev/null
sleep 1
is worker starting || fail "worker running before its ready_log line: $(field worker status)"
wait_for 10 is worker running || fail "worker not running after its ready_log line: $(field worker status)"
wait_for 15 is web running || fail "web not running: $(field web note)"
wait_for 50 grep -q "^failing 2 5$" "$work/calls" || fail "on_crash for failing: $(cat "$work/calls" 2>/dev/null)"
[ "$(field failing restarts)" = 5 ] || fail "failing restarts: $(field failing restarts)"
set +e
portside check > "$work/check"
code=$?
set -e
[ "$code" = 1 ] && grep -q failing "$work/check" || { cat "$work/check"; fail "check exited $code instead of 1 for a crashed server"; }
portside down web failing > /dev/null
sleep 1
[ "$(wc -l < "$work/calls")" = 1 ] || fail "on_crash ran more than once or for a stop: $(cat "$work/calls")"
portside check > /dev/null || fail "check failed with every server that should run ready"

step "daemon logs from the journal"
portside daemon logs -n 50 > "$work/logs"
grep -q "on_crash for failing ran" "$work/logs" || { cat "$work/logs"; fail "daemon logs does not show the journal"; }

step "on_unready, on_recover and retry_after"
portside up flaky retrying > /dev/null 2>&1 || true
wait_for 30 grep -q "^recovered unready$" "$work/events" || fail "on_unready and on_recover for flaky: $(cat "$work/events" 2>/dev/null)"
[ "$(head -1 "$work/events")" = unready ] && [ "$(wc -l < "$work/events")" = 2 ] || fail "flaky events: $(cat "$work/events")"
wait_for 20 grep -q "^retry 2$" "$work/retries" || fail "retry_after for retrying: $(cat "$work/retries" 2>/dev/null)"
[ "$(head -1 "$work/retries")" = "start 1" ] || fail "first report for retrying: $(cat "$work/retries")"
portside down flaky retrying > /dev/null

step "in-place upgrade, with a server still waiting for its ready_log line"
portside up slow > /dev/null
sleep 1
is slow starting || fail "slow is $(field slow status) before its ready_log line"
pid=$(main_pid)
install -m 755 "$next" ~/.local/bin/portside
portside daemon restart
[ "$(main_pid)" = "$pid" ] || fail "upgrade changed the pid: $pid -> $(main_pid)"
is worker running || fail "worker did not survive the upgrade"
is slow starting || fail "the upgrade let slow count as ready before its line: $(field slow status)"
wait_for 10 is slow running || fail "slow not running after its line: $(field slow status)"
portside down slow > /dev/null
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
