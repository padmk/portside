# Portside

Local dev-server manager: Go daemon + CLI (`cmd/portside`) with an embedded React dashboard (`web/`). macOS first, Linux supported; Unix-only (process groups, PTYs).

## Commands

- `make build`: dashboard (`pnpm --dir web build` into `web/dist`) and `bin/portside`.
- `go vet ./...` and `pnpm --dir web build` are the checks; there is no test suite. `pnpm --dir web check:contrast` verifies WCAG AA for every theme.
- `make dev-daemon` (isolated daemon on :7980, config and state in `.dev/`), `make dev-open` (sign the browser in), `make dev-ui` (Vite on http://127.0.0.1:5173 proxying `/api` and `/auth`). The API needs the token in `<state>/auth-token`; browsers get a session cookie through `portside open`.
- Try changes against an isolated instance: `PORTSIDE_CONFIG=/tmp/x/portside.toml PORTSIDE_STATE_DIR=/tmp/x/state` with `port = <free port>` in that config. The user's real daemon runs on :7878; do not stop it or its servers.

## Layout

- `internal/app`: product name, ID, service label, default port, env prefix. The only place the product name is defined.
- `internal/config`: TOML loading and validation (`config.go`), comment-preserving edits of `[servers.<name>]` tables (`edit.go`), starter file, devctl importer.
- `internal/supervisor`: process lifecycle in PTYs (`supervisor.go`), scrollback and log files (`logbuf.go`), login-shell environment (`shellenv.go`), CPU/memory sampling, persisted state and orphan recovery (`state.go`), JSON `View`.
- `internal/server`: HTTP API, WebSockets (`/api/events`, `/api/servers/{name}/terminal`), host/origin guard and `*.localhost` proxy (`server.go`), routes (`routes.go`). The JSON shapes here are the dashboard's contract.
- `internal/daemon`: daemon process (lock, listeners, signals; writes `<state>/daemon.port` so clients find a daemon whose config moved to another port). `internal/client`: CLI-to-daemon HTTP client with auto-spawn.
- `internal/cli`: commands. `internal/mcpserver`: MCP tools over stdio. `internal/service`: launchd/systemd. `internal/discover`: project detection. `internal/ports`: listener lookup via `lsof`/`ss`.
- `web/`: dashboard (Vite, React, xterm.js); `web/embed.go` embeds `web/dist`. Themes: `web/src/styles/themes/*.css` are copied unchanged from the shared ui-theme catalog; Portside's own per-theme tokens live in `web/src/styles/semantic.css` and terminal palettes in `web/src/theme/terminal.ts`.
- `macos/`: Swift menu bar app (AppKit status item + SwiftUI panel, WebKit dashboard window); `scripts/build-macos-app.sh` assembles `dist/macos/Portside.app` with the Go binary in `Contents/Helpers/portside`. `make app-install` installs it.
- `packaging/homebrew/portside.rb.tmpl`: cask rendered by the release workflow.
- `design/`: app icons (`design/brand`: tug light/dark), compressed concept and icon explorations (`design/explorations`; full-size originals live untracked in `design/originals`), screenshots used by the README (`scripts/screenshots.sh`; `--portfolio DIR` writes light/dark pairs and renders the menu bar panel offscreen with the app's `--render-panel=PATH` flag). The menu bar glyph is drawn in code (`macos/Sources/Portside/StatusIcon.swift`).

## Constraints

- Never use the em dash character in code, comments, UI copy (English or Polish) or docs.
- Keep the product name out of identifiers; derive user-visible names and env vars from `internal/app`.
- Config edits must go through `internal/config` so comments survive and invalid text never reaches disk.
- Process termination always targets the process group (`-pgid`): SIGINT, then SIGTERM, then SIGKILL.
- pnpm is pinned to 12.6.0 with `minimumReleaseAge: 1440`; ask before adding `trustPolicy` exceptions.
