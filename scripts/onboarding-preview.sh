#!/usr/bin/env bash
# Shows the macOS app as a new user sees it, next to the running copy and
# without touching it: a second app instance with a fresh config, state
# directory and daemon on a free port, and an empty temporary home folder, so
# the coding agents have no MCP server or skill yet and the dashboard's
# getting-started page offers them; whatever it connects lands there. The agents' commands come from
# this shell's PATH, the projects folder is the one a new install would pick
# in your real home, and the first-launch preferences are reset for that
# instance only. Press Enter to stop the preview's daemon, quit its app and
# move the temporary directory to the Trash. Launch at Login and Install
# Command Line Tool act on the real system there too.
#
#   scripts/onboarding-preview.sh            # dist/macos/Portside.app, else /Applications
#   scripts/onboarding-preview.sh <app>
set -euo pipefail
cd "$(dirname "$0")/.."

app="${1:-dist/macos/Portside.app}"
[ -d "$app" ] || app=/Applications/Portside.app
helper="$app/Contents/Helpers/portside"
[ -x "$helper" ] || { echo "no app at $app; build one with make app" >&2; exit 1; }
app="$(cd "$app" && pwd)"

port=7990
while lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; do port=$((port + 1)); done

dir="$(mktemp -d /tmp/onboarding-preview.XXXXXX)"
home="$dir/home"
# The agents count as installed when their config directory exists.
mkdir -p "$home/.claude" "$home/.codex" "$home/.gemini"
export PORTSIDE_CONFIG="$dir/config/portside.toml" PORTSIDE_STATE_DIR="$dir/state"
"$helper" init >/dev/null
# The starter file names the projects folder from the real home; spell it out
# for the temporary one. It leaves the port to the daemon, which would refuse
# to move away from the running daemon's port, so the preview gets its own.
{
  printf 'port = %s\n' "$port"
  sed "s#^root = \"~/#root = \"$HOME/#" "$PORTSIDE_CONFIG"
} > "$PORTSIDE_CONFIG.new"
mv "$PORTSIDE_CONFIG.new" "$PORTSIDE_CONFIG"

before="$(pgrep -f "$app/Contents/MacOS/" || true)"
# Arguments override the app's preferences for this instance only.
open -n -a "$app" \
  --env "PORTSIDE_CONFIG=$PORTSIDE_CONFIG" --env "PORTSIDE_STATE_DIR=$PORTSIDE_STATE_DIR" \
  --env "HOME=$home" --env "PATH=$PATH" \
  --env "CLAUDE_CONFIG_DIR=$home/.claude" --env "CODEX_HOME=$home/.codex" --env "GEMINI_CLI_HOME=$home" \
  --args -launchedBefore NO
pid=""
for _ in $(seq 50); do
  pid="$(pgrep -f "$app/Contents/MacOS/" | grep -vxF "$before" | tail -n 1 || true)"
  [ -n "$pid" ] && break
  sleep 0.1
done

cat <<EOF
Preview running (app pid ${pid:-unknown}, daemon on :$port, files in $dir).
It has a second menu bar icon; the one you use every day keeps running.
Press Enter to stop the preview.
EOF
read -r _

"$helper" daemon stop >/dev/null 2>&1 || true
[ -n "$pid" ] && kill "$pid" 2>/dev/null || true
mv "$dir" "$HOME/.Trash/$(basename "$dir")"
echo "Stopped; $dir is in the Trash."
