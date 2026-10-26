#!/usr/bin/env bash
# Upgrades a daemon of the latest published release in place to <binary>,
# with servers running, and checks that the daemon keeps its pid, runs the
# new version and keeps the servers ready. It runs an isolated instance in a
# temporary directory (moved to the Trash afterwards where `trash` exists), so
# it is safe next to an installed Portside.
#
# Usage: scripts/upgrade-from-release.sh <binary> [<owner>/<repo>]
set -euo pipefail

new=$(realpath "$1")
repo=${2:-${GITHUB_REPOSITORY:-padmk/portside}}
fail() { echo "FAIL: $*" >&2; exit 1; }
json() { python3 -c "import sys,json; print(json.load(sys.stdin)$1)"; }

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case $(uname -m) in
	x86_64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) fail "unsupported architecture $(uname -m)" ;;
esac
auth=()
[ -n "${GH_TOKEN:-}" ] && auth=(-H "Authorization: Bearer $GH_TOKEN")
tag=$(curl -fsSL ${auth[@]+"${auth[@]}"} "https://api.github.com/repos/$repo/releases/latest" | json '["tag_name"]')

work=$(mktemp -d)
curl -fsSL -o "$work/old.tar.gz" "https://github.com/$repo/releases/download/$tag/portside_${tag#v}_${os}_${arch}.tar.gz"
tar -xzf "$work/old.tar.gz" -C "$work" portside
old="$work/portside"
export PORTSIDE_CONFIG="$work/portside.toml" PORTSIDE_STATE_DIR="$work/state"
cat > "$PORTSIDE_CONFIG" <<CONFIG
port = 7957
root = "$work"

[servers.web]
cmd = "python3 -m http.server {PORT} --bind 127.0.0.1"
health = "/"

[servers.worker]
cmd = "while true; do echo tick; sleep 1; done"
port = 0
CONFIG
cleanup() {
	"$new" daemon stop > /dev/null 2>&1 || true
	if command -v trash > /dev/null; then trash "$work"; fi
}
trap cleanup EXIT

echo "== $tag ($("$old" version)) with two servers"
"$old" up web worker --wait > /dev/null
pid=$("$old" daemon info | json '["pid"]')

echo "== upgrade in place to $("$new" version)"
"$new" daemon restart --in-place
info=$("$new" daemon info)
[ "$(echo "$info" | json '["pid"]')" = "$pid" ] || fail "the upgrade changed the daemon's pid"
[ "$(echo "$info" | json '["version"]')" = "$("$new" version | cut -d' ' -f2)" ] || fail "the daemon runs $(echo "$info" | json '["version"]')"
"$new" check web worker || fail "servers not ready after the upgrade"
echo "upgrade from $tag passed"
