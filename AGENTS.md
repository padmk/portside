# Working on Portside

Local dev-server manager: Go daemon + CLI (`cmd/portside`) with an embedded React dashboard (`web/`) and a Swift menu bar app (`macos/`). Built for macOS 26 or later and Linux (x86-64, arm64), both first-class; the menu bar app is macOS-only, and on Linux the dashboard runs in a browser. Keep both platforms working: Linux paths compile and are vetted, and CI runs the systemd user service end to end. Unix-only (process groups, PTYs).

## Commands

```sh
make build        # dashboard (pnpm --dir web build into web/dist) and bin/portside
make check        # go vet, go test, the dashboard build, the license notices check
make notices      # regenerate THIRD_PARTY_NOTICES.txt after changing Go or npm dependencies or adding a theme
make dev-daemon   # isolated daemon on :7980, config and state in .dev/
make dev-open     # sign the browser in; prints the Vite address on the dev daemon's dashboard host
make dev-ui       # Vite on :5173 proxying /api and /auth; open it at http://<dashboard host>:5173
pnpm --dir web check:contrast   # WCAG AA for every theme
```

- `PORTSIDE_DEV_ORIGIN=5173` lets the dashboard host at that port write to the dev daemon. The dashboard host is new on each cold start of a daemon (`<state>/dashboard-host`).
- Clients sign every request with the token in `<state>/auth-token` (never sent); for a manual call, `curl -H "Authorization: Bearer $(cat <state>/auth-token)"` works.
- `scripts/upgrade-from-release.sh <binary>` upgrades the latest published release in place to that binary on an isolated instance.
- `scripts/linux-service-e2e.sh <binary> <other binary>` runs the systemd user service end to end with the default config and state locations: run it only on a disposable machine such as an OrbStack one, never on the owner's.
- `scripts/bench-idle.sh` measures an idle isolated daemon; its numbers are in `docs/how-it-works.md` "Periodic checks".

CI (`.github/workflows/ci.yml`) runs on Linux for pushes to main and pull requests: `make build`, the notices check, `go vet`, `go test -race`, `pnpm audit`, `govulncheck`, the contrast check, the Linux service end to end and an upgrade from the latest release. The macOS job builds the app, runs `go test` and the upgrade. CodeQL scans Go and TypeScript. macOS runners are expensive, so push finished work in batches. Releases run from a `v*` tag with a `CHANGELOG.md` section; see [docs/development.md](docs/development.md#releases).

## Constraints

- On a Mac where Portside is installed with Homebrew (the owner's machines), never run `make app-install` or `make install`: they overwrite the installed app and command. Use `make dev-daemon`, `scripts/onboarding-preview.sh` or an isolated instance: `PORTSIDE_CONFIG=/tmp/x/portside.toml PORTSIDE_STATE_DIR=/tmp/x/state` with `port = <free port>` in that config. The user's real daemon runs on :7878; do not stop it or its servers.
- Never use the em dash character in code, comments, UI copy (English or Polish) or docs.
- Keep the product name out of identifiers; derive user-visible names and env vars from `internal/app`, the only place the name is defined.
- macOS builds target Apple Silicon only (arm64) and macOS 26 or later; keep `macos/Package.swift`, `macos/Resources/Info.plist`, the cask template, the CI runners, `scripts/build-macos-app.sh` and `.goreleaser.yaml` in line with the README requirements.
- Config edits must go through `internal/config` so comments survive and invalid text never reaches disk.
- Files written into the config or state directories use `paths.FileMode` and directories `paths.DirMode`: configs can hold secrets in `env` and logs can hold credentials. State files are replaced through `paths.WriteFile` (a fresh private temporary file, then rename); the config through `internal/config`.
- Process termination always targets the process group (`-pgid`): SIGINT, then SIGTERM, then SIGKILL.
- Tests need no Docker, Tailscale, external network or real config; readiness checks use isolated loopback listeners.
- pnpm is pinned to 12.6.0 with `minimumReleaseAge: 1440`; ask before adding `trustPolicy` exceptions.
- Third-party actions in `.github/workflows` are pinned to commit SHAs with the tag in a comment, GoReleaser and govulncheck to exact versions; update a pin together with its comment. CI and releases use `go.mod`'s `toolchain` line; raise it for Go security releases that govulncheck reports.

## Code and docs

| Path | Responsibility |
|---|---|
| `cmd/portside`, `internal/cli` | Entry point and commands |
| `internal/daemon`, `internal/server` | The daemon process, port choice and upgrades in place; the HTTP API, WebSockets, auth and the `*.localhost` proxy |
| `internal/supervisor` | Process lifecycle in PTYs, restarts, tasks, schedules, file watching, memory limits |
| `internal/config`, `internal/discover` | TOML loading, validation and comment-preserving edits; project detection for `scan` |
| `internal/docker` | Docker Engine client and the container monitor |
| `internal/mcpserver`, `internal/agentsetup`, `internal/agentskill` | The MCP server, coding-agent setup and the agent skill |
| `internal/remote`, `internal/share`, `internal/signing`, `internal/tailnet` | Other computers over Tailscale |
| `web/` | Dashboard (Vite, React, xterm.js), embedded by `web/embed.go` |
| `macos/` | The menu bar app |
| `packaging/`, `.goreleaser.yaml`, `.github/workflows` | Homebrew cask and formula templates, release builds |

[docs/code-map.md](docs/code-map.md) maps every package and its important files and lists what the tests cover; read the entries for the area you change. [docs/development.md](docs/development.md) covers building next to an installed Portside, CI, releases and license notices. [docs/how-it-works.md](docs/how-it-works.md) explains processes, state and upgrades in place, and [docs/security.md](docs/security.md) the security model.

`README.md` is the landing page for people and for agents that install or use Portside; the user documentation lives in `docs/` by topic, and each fact stays in one place. Keep `SECURITY.md`'s scope in line with `docs/security.md`. Update the README, the docs, `docs/code-map.md` and this file with any change to what users or agents get or to where code lives.
