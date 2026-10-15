#!/usr/bin/env bash
# Regenerates design/screenshots/ from a throwaway demo: fake projects under a
# temporary home directory (so paths read ~/code/acme/...), a daemon on :7990
# and headless Chrome. Needs Google Chrome and Node 24; touches nothing else.
#
#   scripts/screenshots.sh           # dashboard screenshots
#   scripts/screenshots.sh --menu    # also the macOS menu bar panel (opens it on screen)
set -euo pipefail
cd "$(dirname "$0")/.."
REPO="$PWD"
REAL_HOME="$HOME"

DEMO="$(mktemp -d /tmp/portside-demo.XXXXXX)"
HOMEDIR="$DEMO/home"
PS="$DEMO/portside"
go build -o "$PS" ./cmd/portside # before HOME changes, so the module cache is reused

for p in acme/web acme/api acme/worker acme/docs studio/app blog; do mkdir -p "$HOMEDIR/code/$p"; done
# Simple project logos, picked up as favicons.
logo() { mkdir -p "$HOMEDIR/code/$1/public"; printf '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="8" fill="%s"/><text x="16" y="22" font-family="Helvetica" font-weight="700" font-size="17" text-anchor="middle" fill="#fff">%s</text></svg>' "$2" "$3" > "$HOMEDIR/code/$1/public/favicon.svg"; }
logo acme/web '#6d5bd0' 'A'
logo acme/docs '#2e8c94' 'D'
logo studio/app '#d96350' 'S'
logo blog '#e3a33b' 'B'

# Stand-ins for pnpm and uv so the commands read like real projects; each
# runs the fake server configured through DEMO_* variables.
mkdir -p "$DEMO/bin"
for tool in pnpm uv; do
  printf '#!/bin/sh\nexec python3 %s --kind "$DEMO_KIND" --name "$DEMO_NAME" --port "${PORT:-0}" --crash-after "${DEMO_CRASH:-0}"\n' "$REPO/scripts/demo/server.py" > "$DEMO/bin/$tool"
  chmod +x "$DEMO/bin/$tool"
done

cat > "$DEMO/portside.toml" <<TOML
root = "~/code"
port = 7990
restore = false
notify = false
path = ["$DEMO/bin"]

[servers.web]
dir = "acme/web"
cmd = "pnpm dev --port {PORT}"
port = 3101
group = "acme"
depends_on = ["api"]
env = { DEMO_KIND = "next", DEMO_NAME = "web" }

[servers.api]
dir = "acme/api"
cmd = "uv run uvicorn api.main:app --reload --port {PORT}"
port = 3102
group = "acme"
env = { DEMO_KIND = "api", DEMO_NAME = "api" }

[servers.worker]
dir = "acme/worker"
cmd = "pnpm worker"
port = 0
group = "acme"
env = { DEMO_KIND = "worker", DEMO_NAME = "worker" }

[servers.docs]
dir = "acme/docs"
cmd = "pnpm astro dev --port {PORT}"
port = 3103
group = "acme"
env = { DEMO_KIND = "docs", DEMO_NAME = "docs" }

[servers.studio]
dir = "studio/app"
cmd = "pnpm dev --port {PORT} --strictPort"
port = 3104
env = { DEMO_KIND = "vite", DEMO_NAME = "studio" }

[servers.blog]
dir = "blog"
cmd = "pnpm dev --port {PORT}"
port = 3105
env = { DEMO_KIND = "next", DEMO_NAME = "blog", DEMO_CRASH = "5" }
TOML

export HOME="$HOMEDIR" SHELL=/bin/bash PORTSIDE_CONFIG="$DEMO/portside.toml" PORTSIDE_STATE_DIR="$DEMO/state"
cleanup() { "$PS" daemon stop >/dev/null 2>&1 || true; }
trap cleanup EXIT

"$PS" daemon start >/dev/null
"$PS" up --all >/dev/null || true
sleep 14 # let logs, sparklines and the crash happen

# Chrome keeps cookies in the real keychain, so it runs with the real home.
HOME="$REAL_HOME" node "$REPO/scripts/screenshots.mjs" "$DEMO" "$REPO/design/screenshots"

if [ "${1:-}" = "--menu" ] && [ -d "$REPO/dist/macos/Portside.app" ]; then
  open -n --env HOME="$HOMEDIR" --env PORTSIDE_CONFIG="$PORTSIDE_CONFIG" --env PORTSIDE_STATE_DIR="$PORTSIDE_STATE_DIR" \
    "$REPO/dist/macos/Portside.app" --args --show-menu
  sleep 6
  # Top right corner of the main display, where the panel opens.
  screencapture -x "$DEMO/screen.png"
  pkill -f "$REPO/dist/macos/Portside.app/Contents/MacOS/Portside" || true
  echo "Menu bar panel captured to $DEMO/screen.png; crop it into design/screenshots/menubar.png."
fi
echo "Screenshots written to design/screenshots/ (demo in $DEMO)"
