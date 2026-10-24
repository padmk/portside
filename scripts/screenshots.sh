#!/usr/bin/env bash
# Regenerates design/screenshots/ from a throwaway demo: fake projects under a
# temporary home directory (so paths read ~/code/acme/...), a daemon on :7990
# (SHOT_PORT picks another)
# and headless Chrome. Needs Google Chrome and Node 24. With a running Docker
# engine it also starts two small Compose projects, acme and studio
# (postgres:17-alpine and redis:7-alpine, published on 127.0.0.1:55432 and
# :56379), refusing to run when projects with those names exist, and removes
# them, volumes included, on exit; it touches nothing else.
#
# With dist/macos/Portside.app built it also renders the menu bar panel.
#
#   scripts/screenshots.sh                    # README screenshots in design/screenshots
#   scripts/screenshots.sh --portfolio DIR    # light/dark pairs and the panel into DIR
set -euo pipefail
cd "$(dirname "$0")/.."
REPO="$PWD"
REAL_HOME="$HOME"

shot_port="${SHOT_PORT:-7990}"
if lsof -nP -iTCP:"$shot_port" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "port $shot_port is in use (a demo daemon left running?); stop it first or set SHOT_PORT" >&2
  exit 1
fi
DEMO="$(mktemp -d /tmp/portside-demo.XXXXXX)"
HOMEDIR="$DEMO/home"
PS="$DEMO/portside"
APP="$REPO/dist/macos/Portside.app"
if [ -x "$APP/Contents/Helpers/portside" ]; then
  # Same binary as the app, so the app sees a daemon of its own version.
  cp "$APP/Contents/Helpers/portside" "$PS"
else
  go build -o "$PS" ./cmd/portside # before HOME changes, so the module cache is reused
fi

for p in acme/web acme/api acme/worker shop/storefront shop/admin studio/app blog landing; do mkdir -p "$HOMEDIR/code/$p"; done
# The coding agents count as installed, with nothing connected yet, for the Settings shot.
mkdir -p "$HOMEDIR/.claude" "$HOMEDIR/.codex"
# Simple project logos, picked up as favicons.
logo() { mkdir -p "$HOMEDIR/code/$1/public"; printf '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="8" fill="%s"/><text x="16" y="22" font-family="Helvetica" font-weight="700" font-size="17" text-anchor="middle" fill="#fff">%s</text></svg>' "$2" "$3" > "$HOMEDIR/code/$1/public/favicon.svg"; }
logo acme/web '#6d5bd0' 'A'
logo studio/app '#d96350' 'S'
logo blog '#e3a33b' 'B'
logo landing '#c2577a' 'L'

# Stand-ins for pnpm and uv so the commands read like real projects; each
# runs the fake server configured through DEMO_* variables.
mkdir -p "$DEMO/bin"
for tool in pnpm uv; do
  printf '#!/bin/sh\nexec python3 %s --kind "$DEMO_KIND" --name "$DEMO_NAME" --port "${PORT:-0}" --crash-after "${DEMO_CRASH:-0}" --mem "${DEMO_MEM:-0}"\n' "$REPO/scripts/demo/server.py" > "$DEMO/bin/$tool"
  chmod +x "$DEMO/bin/$tool"
done

cat > "$DEMO/portside.toml" <<TOML
root = "~/code"
port = $shot_port
restore = false
notify = false
path = ["$DEMO/bin"]

[servers.web]
dir = "acme/web"
cmd = "pnpm dev --port {PORT}"
port = 3101
group = "acme"
depends_on = ["api"]
env = { DEMO_KIND = "next", DEMO_NAME = "web", DEMO_MEM = "410" }

[servers.api]
dir = "acme/api"
cmd = "uv run uvicorn api.main:app --reload --port {PORT}"
port = 3102
group = "acme"
env = { DEMO_KIND = "api", DEMO_NAME = "api", DEMO_MEM = "120" }

[servers.worker]
dir = "acme/worker"
cmd = "pnpm worker"
port = 0
group = "acme"
env = { DEMO_KIND = "worker", DEMO_NAME = "worker", DEMO_MEM = "85" }

[servers.storefront]
dir = "shop/storefront"
cmd = "pnpm dev --port {PORT}"
port = 3201
group = "shop"
env = { DEMO_KIND = "next", DEMO_NAME = "storefront", DEMO_MEM = "380" }

[servers.admin]
dir = "shop/admin"
cmd = "pnpm dev --port {PORT} --strictPort"
port = 3202
group = "shop"
env = { DEMO_KIND = "vite", DEMO_NAME = "admin", DEMO_MEM = "140" }

[servers.studio]
dir = "studio/app"
cmd = "pnpm dev --port {PORT} --strictPort"
port = 3104
env = { DEMO_KIND = "vite", DEMO_NAME = "studio", DEMO_MEM = "150" }

[servers.blog]
dir = "blog"
cmd = "pnpm dev --port {PORT}"
port = 3105
env = { DEMO_KIND = "next", DEMO_NAME = "blog", DEMO_CRASH = "5", DEMO_MEM = "260" }

[servers.landing]
dir = "landing"
cmd = "pnpm astro dev --port {PORT}"
port = 3106
env = { DEMO_KIND = "docs", DEMO_NAME = "landing", DEMO_MEM = "120" }
TOML

# Compose projects for the containers views, when an engine is running: the
# api's database and cache, and a studio database whose health check fails.
# The docker CLI keeps the real Docker config, so it finds the active engine.
export DOCKER_CONFIG="$REAL_HOME/.docker"
unset COMPOSE_FILE COMPOSE_PROJECT_NAME COMPOSE_PROFILES COMPOSE_ENV_FILES
DEMO_PROJECTS="acme:acme/api studio:studio/app"
# compose_demo NAME DIR ARGS... runs Compose for one demo project only.
compose_demo() { local name="$1" dir="$HOMEDIR/code/$2"; shift 2; docker compose -p "$name" -f "$dir/compose.yaml" --project-directory "$dir" "$@"; }
DOCKER=0
if docker info >/dev/null 2>&1; then
  for entry in $DEMO_PROJECTS; do
    if [ -n "$(docker ps -aq --filter "label=com.docker.compose.project=${entry%%:*}")" ]; then
      echo "a Compose project named ${entry%%:*} already exists; the demo would reuse it, so stop" >&2
      exit 1
    fi
  done
  DOCKER=1
  cat > "$HOMEDIR/code/acme/api/compose.yaml" <<'YAML'
name: acme
services:
  db:
    image: postgres:17-alpine
    environment: { POSTGRES_PASSWORD: demo, POSTGRES_DB: acme }
    ports: ["127.0.0.1:55432:5432"]
    healthcheck: { test: ["CMD-SHELL", "pg_isready -U postgres"], interval: 2s, retries: 15 }
  cache:
    image: redis:7-alpine
    ports: ["127.0.0.1:56379:6379"]
    healthcheck: { test: ["CMD", "redis-cli", "ping"], interval: 2s, retries: 15 }
YAML
  cat > "$HOMEDIR/code/studio/app/compose.yaml" <<'YAML'
name: studio
services:
  db:
    image: postgres:17-alpine
    environment: { POSTGRES_PASSWORD: demo }
    healthcheck: { test: ["CMD-SHELL", "psql -U postgres -d studio -c 'select 1'"], interval: 2s, retries: 2, start_period: 1s }
YAML
fi

export HOME="$HOMEDIR" SHELL=/bin/bash PORTSIDE_CONFIG="$DEMO/portside.toml" PORTSIDE_STATE_DIR="$DEMO/state"
cleanup() {
  # Demo app instances would start the demo daemon again, so they go first.
  pkill -f "$REPO/dist/macos/Portside.app/Contents/MacOS/Portside" 2>/dev/null || true
  "$PS" daemon stop >/dev/null 2>&1 || true
  if [ "$DOCKER" = 1 ]; then
    # Only projects whose containers all run from the demo directory.
    for entry in $DEMO_PROJECTS; do
      dirs="$(docker ps -aq --filter "label=com.docker.compose.project=${entry%%:*}" | xargs -r docker inspect --format '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' 2>/dev/null | sort -u)"
      case "$dirs" in "$DEMO"/*|"$(cd "$DEMO" && pwd -P)"/*) [ "$(printf '%s\n' "$dirs" | wc -l)" -eq 1 ] && compose_demo "${entry%%:*}" "${entry#*:}" down -v -t 0 >/dev/null 2>&1 || true ;; esac
    done
  fi
}
trap cleanup EXIT

if [ "$DOCKER" = 1 ]; then
  for entry in $DEMO_PROJECTS; do compose_demo "${entry%%:*}" "${entry#*:}" up -d >/dev/null 2>&1 || true; done
fi
"$PS" daemon start >/dev/null
# The shop group and landing stay stopped, so every state is on screen.
"$PS" up web api worker studio blog >/dev/null || true
sleep 14 # let logs, sparklines and the crash happen

MODE="${1:-}"
OUT="$REPO/design/screenshots"
SET=readme
if [ "$MODE" = "--portfolio" ]; then
  OUT="${2:?usage: --portfolio DIR}"
  SET=portfolio
  mkdir -p "$OUT"
fi
# Chrome keeps cookies in the real keychain, so it runs with the real home.
HOME="$REAL_HOME" node "$REPO/scripts/screenshots.mjs" "$DEMO" "$OUT" "$SET" "$shot_port"

if [ -d "$APP" ]; then
  # The menu bar panel, drawn offscreen by a second app instance on the demo
  # daemon. README gets English dark/light; the portfolio both looks.
  looks="dark light"; lang="${SHOT_LANG:-pl}"
  [ "$SET" = readme ] && lang=en
  for look in $looks; do
    if [ "$SET" = portfolio ]; then
      suffix=""; [ "$look" = light ] && suffix="-light"
      panel="$OUT/portside-menubar$suffix.png"
    else
      panel="$OUT/menubar-$look.png"
    fi
    "$APP/Contents/MacOS/Portside" -AppleLanguages "($lang)" --appearance="$look" --render-panel="$panel" >/dev/null 2>&1 || true
  done
fi
echo "Screenshots written to $OUT (demo in $DEMO)"
