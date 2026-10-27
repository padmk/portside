<p align="center">
  <img src="design/brand/tug-256.png" width="128" alt="">
</p>

<h1 align="center">portside</h1>

<p align="center">Keep your local dev servers running, in one place.</p>

<p align="center">
  <a href="https://github.com/padmk/portside/releases/latest"><img src="https://img.shields.io/github/v/release/padmk/portside" alt="Latest release"></a>
  <img src="https://img.shields.io/badge/macOS-26%2B%20%C2%B7%20Apple%20Silicon-black" alt="macOS 26 or later on Apple Silicon">
  <a href="docs/linux-server.md"><img src="https://img.shields.io/badge/Linux-x86--64%20%C2%B7%20arm64-black" alt="Linux on x86-64 and arm64"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/padmk/portside" alt="MIT license"></a>
  <a href="https://foreverbeta.app/portside/"><img src="https://img.shields.io/badge/website-foreverbeta.app-0a7ea4" alt="Website"></a>
</p>

<p align="center">
  <a href="#install"><b>Install</b></a> ·
  <a href="#quick-start"><b>Quick start</b></a> ·
  <a href="#features"><b>Features</b></a> ·
  <a href="#documentation"><b>Docs</b></a> ·
  <a href="CHANGELOG.md"><b>Changelog</b></a>
</p>

<p align="center">
  <img src="design/screenshots/overview-dark.png" width="900" alt="Portside dashboard: server cards grouped by project, with running, stopped and crashed servers">
</p>

**Every dev server you run, in one place.** Next.js, Vite, Python APIs, `docker compose` stacks and workers start in the background, keep their ports, and live in your menu bar, a dashboard and your terminal.

- **Stop juggling terminal tabs.** One click or `portside up shop` starts a project with everything it depends on; each server keeps its port and gets a `name.localhost` address.
- **Know right away when something breaks.** A crash sends a notification with the exit code and the last lines of the log, and so does a container that turns unhealthy.
- **Your coding agents use the same servers.** Claude Code and Codex reuse the server that already runs instead of starting another copy on a random port, and read its logs when something fails.
- **Reach the servers on your other Mac.** Pair your laptop with the desktop that runs your projects and manage its servers, logs and containers from anywhere on your tailnet; its sites open on your laptop as if they ran there.
- **Free, open source and local.** No account and no telemetry; nothing leaves your machine unless you pair another computer or use a remote Docker engine.

<p align="center"><img src="design/screenshots/detail.png" width="900" alt="A server's live terminal with every server one click away in the side rail"></p>

<p align="center"><img src="design/screenshots/crashed.png" width="900" alt="A crashed server with its exit code and the error that caused it"></p>

<p align="center"><img src="design/screenshots/containers.png" width="900" alt="A server's Compose containers with their state, usage and live logs"></p>

<p align="center"><img src="design/screenshots/agents.png" width="900" alt="Settings on the coding agents: Claude Code and Codex with their MCP server and skill, Connect, and the prompt to copy for an agent"></p>

<p align="center"><picture><source media="(prefers-color-scheme: dark)" srcset="design/screenshots/menubar-dark.png"><img src="design/screenshots/menubar-light.png" width="420" alt="The menu bar panel: every server with its project icon, state, memory and port, grouped by project"></picture></p>

## Install

With [Homebrew](https://brew.sh). On **macOS** 26 or later (Apple Silicon), the menu bar app and the `portside` command:

```sh
brew install --cask padmk/tap/portside
```

On **macOS or Linux** (x86-64, arm64), the command and dashboard, built from source:

```sh
brew install --formula padmk/tap/portside
```

On Linux, `portside service install` then starts the daemon at login, or at boot with lingering; [Linux server](docs/linux-server.md) is the guide for a machine without a desktop. Without the menu bar app, the dashboard opens in your browser (`portside open`). The cask and the formula both install `portside`, so install only one of them.

The app is not notarized by Apple yet, so the cask removes the quarantine flag to let macOS start it. You rely instead on the public source and on release files built by GitHub Actions with a verifiable attestation; [Trust](docs/security.md#trust) says what that covers and how to check a download. The formula avoids the prebuilt app.

<details>
<summary>Other ways to install, update and uninstall</summary>

**Download.** Get `Portside-<version>.zip` from the [releases](https://github.com/padmk/portside/releases), move `Portside.app` to Applications, and choose **Settings > Install Command Line Tool** in its menu. The app is signed ad hoc and not notarized, so macOS blocks the first launch of a downloaded copy: open it once, then choose **Open Anyway** in **System Settings > Privacy & Security** and confirm ([Apple's instructions](https://support.apple.com/en-us/102445)).

**Release archives.** `portside_<version>_<os>_<arch>.tar.gz` from the [releases](https://github.com/padmk/portside/releases) holds the `portside` command for Linux (x86-64, arm64) or macOS; put it on your `PATH`. The formula builds the same command from the release's tagged source with Homebrew's Go, Node and pnpm, which it needs only for the build.

**From source** (Go 1.26 or later, Node 24 and pnpm 12):

```sh
git clone https://github.com/padmk/portside
cd portside
make install            # builds the dashboard and installs ~/.local/bin/portside
make app-install        # macOS: builds Portside.app and installs it in /Applications
```

**Browser.** The dashboard is served at its own `*.localhost` host, so it needs a browser that resolves `*.localhost`: Chrome, Firefox, or Safari on macOS 26 and later (the app's dashboard window uses the same WebKit).

**Updating.** Servers keep running through an update.

- With the app: `brew upgrade --cask portside`. Homebrew quits the app, installs the new one and reopens it (Homebrew 5.1.12 or later), and the app upgrades the running daemon in place. `make app-install` does the same for a build from source. When the app cannot upgrade the daemon in place, its **Restart** banner restarts it ([Upgrades in place](docs/how-it-works.md#upgrades-in-place)).
- Without the app (`brew upgrade portside` for the formula, a release download, `make install`): run `portside daemon restart` with the new binary. `portside status` and `portside doctor` say when the daemon is older than the command.

**Uninstalling.** `brew uninstall` leaves the daemon and its servers running, so stop them first with `portside daemon stop`, or `portside service uninstall` if you installed the login service (it stops the daemon too). `brew uninstall --zap --cask portside` stops them itself and trashes the config and state.

</details>

## Quick start

On macOS, open Portside: its first window offers to find your projects and connect your coding agents. From the terminal:

```sh
portside scan --add     # find projects in your projects folder and add them
portside up web --wait  # start one server and wait until it is ready
portside open           # sign in and open the dashboard
portside setup          # connect Claude Code and Codex
```

Any command starts the daemon on demand, so there is nothing to launch first. Servers live in `~/.config/portside/portside.toml`:

```toml
root = "~/dev"

[servers.web]
dir = "shop/apps/web"
cmd = "pnpm dev --port {PORT}"
port = 3001
group = "shop"
```

## Features

One static binary runs a daemon on `127.0.0.1`; the menu bar app, the dashboard, the CLI (with `--json` for scripts) and the MCP server all talk to it.

- **One list for every project.** A single TOML file describes servers across all your repositories, with groups (`portside up shop`) and dependencies (`api` waits for `db`). `portside scan` finds Node, Deno, Python, Ruby, PHP, Elixir, Java, .NET, Go, Rust and Compose projects and proposes the commands.
- **Stable ports and clean URLs.** Each server keeps its port and is also reachable at `http://<name>.localhost:7878` (the daemon's port), WebSockets and hot reload included.
- **Real terminals and accurate status.** Servers run in a PTY with live output, search and `portside attach`. `starting` lasts until the port answers, a server that stops answering is marked, crashes show the exit code, and foreign processes holding a port are named.
- **Survives crashes, restarts and updates.** A crashed server starts again with backoff (`restart`, `on-failure` by default); once it gives up, a notification or your own `on_crash` command reports it, and `on_unready` reports a server that stops answering. Servers keep running through a daemon upgrade and come back after a reboot.
- **Tasks, schedules, memory limits and file watching.** Tasks that run to completion before the servers that need them (`oneshot`), crontab `schedule`s, a `memory_limit` for all servers or one, and restarts when watched files change (`watch`) ([Configuration](docs/configuration.md#tasks)).
- **Containers.** Compose containers appear next to their server with state, health, logs, a shell and recreate, on Docker Desktop, OrbStack, Colima, Podman and remote engines.
- **Coding agents.** An MCP server and a skill for Claude Code, Codex and other MCP clients: `ensure_server` starts a server if needed and returns its URL, agents read logs instead of guessing, and `--read-only` limits them to looking.
- **Other computers.** Pair your computers over Tailscale with a one-time code; every request is signed with the paired device's key. While connected, the dashboard, the CLI (`--on <computer>`) and agents (`computer`) act on the other machine, and its servers' ports are forwarded here ([Other computers](docs/other-computers.md)).

## Documentation

- [Configuration](docs/configuration.md): every key, the environment and version managers, the daemon's port, project discovery
- [CLI](docs/cli.md): every command
- [Dashboard and menu bar app](docs/dashboard.md)
- [Coding agents](docs/coding-agents.md): setup, other MCP clients, the tools, worktrees
- [Containers](docs/containers.md): engines, ownership, actions
- [Other computers](docs/other-computers.md): pairing and sharing over Tailscale, or with your coding agent's help
- [How it works](docs/how-it-works.md): processes, state, upgrades in place, the login service
- [Linux server](docs/linux-server.md): a machine without a desktop, crash reports, scripts
- [Security model](docs/security.md): what Portside can do as you, what agents can do, how to verify a release
- [Compared with other tools](docs/comparison.md): process-compose, pitchfork, Portless, WhatThePort, Overmind
- [Development](docs/development.md) and the [changelog](CHANGELOG.md)

## License

[MIT](LICENSE). Third-party licenses are listed in `THIRD_PARTY_NOTICES.txt`. Report vulnerabilities as described in [SECURITY.md](SECURITY.md).

<p align="center"><sub>Made by <a href="https://foreverbeta.app">foreverbeta</a></sub></p>
