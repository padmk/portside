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
- `internal/supervisor`: process lifecycle in PTYs (`supervisor.go`), scrollback and log files (`logbuf.go`), environment from the account's login shell (`shellenv.go`; see README "Configuration" for how it is read and refreshed), CPU/memory sampling, persisted state and orphan recovery (`state.go`), JSON `View`.
- `internal/docker`: Docker Engine API client over the active context's socket (`endpoint.go`, `client.go`), the live container monitor with events and stats (`monitor.go`), engine start detection (`engine.go`) and compose recreate jobs (`job.go`). Only that one operation shells out to the docker CLI.
- `internal/server`: HTTP API, WebSockets (`/api/events`, `/api/servers/{name}/terminal`), host/origin guard and `*.localhost` proxy (`server.go`), routes (`routes.go`), plain-text log replies with search and size limits (`loglines.go`). The JSON shapes here are the dashboard's contract.
- `internal/daemon`: daemon process (lock, listeners, signals; writes `<state>/daemon.port` so clients find a daemon whose config moved to another port). `internal/client`: CLI-to-daemon HTTP client with auto-spawn.
- `internal/cli`: commands. `internal/mcpserver`: MCP tools over stdio (servers, containers, logs with `match`). `internal/agentskill`: the agent skill (`skill.md`, a template filled from `internal/app`), installed by `portside skill install` and refreshed by the main daemon on start; keep it in line with the CLI and MCP tools. `internal/service`: launchd/systemd. `internal/discover`: project detection. `internal/ports`: listener lookup via `lsof`/`ss`.
- `internal/server/containers.go`: container routes; logs, shell and job output stream over the same terminal WebSocket protocol as servers. Containers attach to servers by compose working directory in `dockerState`.
- `web/`: dashboard (Vite, React, xterm.js); `web/embed.go` embeds `web/dist`. Themes: `web/src/styles/themes/*.css` are copied unchanged from the shared ui-theme catalog; Portside's own per-theme tokens live in `web/src/styles/semantic.css` and terminal palettes in `web/src/theme/terminal.ts`.
- `macos/`: Swift menu bar app (AppKit status item + SwiftUI panel, WebKit dashboard window); `scripts/build-macos-app.sh` assembles `dist/macos/Portside.app` with the Go binary in `Contents/Helpers/portside`. `make app-install` installs it (update steps in README "Install").
- `packaging/homebrew/portside.rb.tmpl`: cask rendered by the release workflow.
- `design/`: app icons (`design/brand`: tug light/dark), compressed concept and icon explorations (`design/explorations`; full-size originals live untracked in `design/originals`), screenshots used by the README (`scripts/screenshots.sh`; `--portfolio DIR` writes light/dark pairs and renders the menu bar panel offscreen with the app's `--render-panel=PATH` flag). The menu bar glyph is drawn in code (`macos/Sources/Portside/StatusIcon.swift`).

## Constraints

- Never use the em dash character in code, comments, UI copy (English or Polish) or docs.
- Keep the product name out of identifiers; derive user-visible names and env vars from `internal/app`.
- macOS builds target Apple Silicon only (arm64); keep `scripts/build-macos-app.sh` and `.goreleaser.yaml` in line with the README requirements.
- Config edits must go through `internal/config` so comments survive and invalid text never reaches disk.
- Process termination always targets the process group (`-pgid`): SIGINT, then SIGTERM, then SIGKILL.
- pnpm is pinned to 12.6.0 with `minimumReleaseAge: 1440`; ask before adding `trustPolicy` exceptions.
