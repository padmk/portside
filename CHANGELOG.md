# Changelog

## Unreleased

- **Updating is `brew upgrade`:** the menu bar app upgrades a running daemon older than itself in place when it starts or reconnects (Homebrew reopens the app it quit for the upgrade), so servers keep running and no `portside daemon restart` is needed. It tries once per daemon version; a daemon it cannot upgrade in place keeps the **Restart** banner, now with the reason.
- **CLI:** `portside daemon restart --in-place` only upgrades in place and fails instead of restarting the servers. `portside status` and `portside doctor` say when the daemon is older than the command; `daemon info` reports it as `outdated`.

- **Other computers:** a forwarded port is available again at once after the other computer's server goes down and up, after `portside forward on`, a reconnect or a local listener exiting, instead of staying busy here for up to 40 seconds while its closed connections linger (TIME_WAIT). A local listener on the port still takes precedence.
- **Config:** removing the last server of the config file (`portside rm`, the dashboard) no longer leaves a blank line at the end of the file.
- **CLI:** `portside up` right after `portside add` (or `rm`, or an edit in your editor) sees the change: the daemon checks the config file before each request instead of only once a second, so `unknown server or group` no longer appears for a server just added.
- **CLI:** `portside daemon restart` from an older release than the running daemon refuses instead of replacing it, and the daemon refuses such a binary too; `--allow-downgrade` replaces it anyway, and the command and the daemon log then say `downgraded` instead of `upgraded`. Development builds are not compared.
- **CLI:** the `portside computers` hint lists every command `--on` runs (also `attach` and `containers`), worded as in the help and the usage error.
- **Other computers:** `portside forward off <port>` for a port the connected computer does not forward fails and lists the forwarded ports instead of storing the setting; a forwarded port that is busy here can still be turned off.
- **Other computers:** replies relayed from the connected computer carry `X-Content-Type-Options` once instead of twice.
- **CLI:** on a computer connected to another one, `portside status` with no local servers points to `portside --on <computer> status`, and `portside doctor` reports sharing and the connection in one line each while they are in use.

## 0.4.3

- **CLI:** a command the running daemon does not know yet, after an update that has not restarted it, says so and suggests `portside daemon restart` instead of `no such endpoint`.
- **Releases:** a release is published only for a commit of `main` whose CI passed, after the built binary and the app's command have been run; published releases are immutable. CI also audits the dashboard's production dependencies.

## 0.4.2

- **May be reachable from the network:** the mark (and `portside status`, `doctor`, the menu bar panel) says a listener may be reachable, since its addresses do not show what the firewall lets in. The fix fits the tool the command runs, following package scripts (Uvicorn, FastAPI, Flask, Django, Gunicorn, Hypercorn, `http.server`, Astro, Nuxt, Remix, React Router, Rails and Phoenix included), and a Compose fix keeps the container's own port.
- Only this machine's own Tailscale addresses count as `tailnet`; other addresses in `100.64.0.0/10` (also carrier-grade NAT space) count as `network`. `portside doctor` lists published container ports whose exposure it could not check.

## 0.4.1

- **Security:** replies relayed from a connected computer also lose their `Location` header, so none of them can redirect the dashboard, as the security docs state.

## 0.4.0

- **Releases:** the files are built in jobs that cannot publish; a separate job creates the release, and the Homebrew tap token is only available to release tags in a protected environment.
- **Security:** replies relayed from a connected computer are served with a sandboxing Content-Security-Policy and without cookies, site data or redirect headers, so a taken-over computer you connect to cannot run script on this dashboard's origin (for example through a server icon opened in a new tab).
- **macOS app:** links from the dashboard open in the browser only when they are http or https.
- **CLI:** `portside scan --add` asks before adding when run on a terminal (`--yes` skips the question); scripts and agents are unaffected.
- **Other computers:** a switch next to each forwarded port in the **Computers** window, or `portside forward off|on <port>`, turns its forwarding off and on, remembered per computer. A port turned off stops listening, its open connections end, and `<name>.<computer>.localhost` for its server says forwarding is off. Every port is still forwarded by default.
- **Sharing:** the shared computer notifies you when a device pairs and when a paired device connects after being away, and its **Computers** window and `portside share devices` show whether each paired device is connected now or when it last connected.
- **Reachable from the network:** a running server or published container port that listens on all interfaces or another non-loopback address (`"5432:5432"` in Compose, `next dev`, `vite --host`, `app.listen(port)`) is marked **Reachable from network** in the dashboard, with how to bind it to `127.0.0.1` for its tool, in `portside status` and `portside doctor`, and as `exposure` and `networkWarning` in the API and `list_servers`. Tailscale-only listeners are not flagged. `lan = true` in a server's table says it is meant to be reachable and hides the mark for it and its containers ([Security](docs/security.md#servers-reachable-from-the-network)).
- **Agents:** with `portside mcp --dir`, `ensure_server` reuses a server that already runs even when a dependency outside the scope is stopped, since nothing would be started; `restart_server` still refuses then.

## 0.3.1

- **Agents:** with `portside mcp --dir`, `ensure_server` and `restart_server` no longer start servers outside the scope through `depends_on`: when such a dependency does not run, they refuse and name it, so the agent asks you to start it.

- **Benchmark:** `scripts/bench-idle.sh` measures the daemon's idle CPU and `ps` calls with no client, the app's closed panel and an open dashboard, so the numbers in How it works can be checked on any Mac.

## 0.3.0

- **Verifiable releases:** every release file has a GitHub build provenance attestation (`gh attestation verify <file> -R padmk/portside`), and each CLI archive an SPDX SBOM. CodeQL checks the Go and dashboard code, and CI builds the app and runs the Go tests on macOS for every push and pull request.
- **Homebrew formula:** `brew install --formula padmk/tap/portside` builds the command-line tool and dashboard from the tagged source, for those who prefer it to the prebuilt app; it conflicts with the cask.
- **Lighter in the background:** CPU and memory are sampled every 2 s only while a dashboard or the app's panel shows them, otherwise every 30 s; running servers are probed once a second and unchanged state is not sent again. An idle daemon with the app connected used about a third of the CPU in our measurement ([How it works](docs/how-it-works.md)).
- **Fix:** opening a server's log file from the dashboard refuses a name that leads out of the log directory.
- **Agents:** `portside mcp --mode read-only|start|full`: `read-only` (also `--read-only`) offers only the tools that read and never starts the daemon, `start` adds `ensure_server` without stopping or restarting; `--dir <path>` limits the tools to the servers, worktree copies and containers of those projects, and `portside setup --mcp-mode` registers a mode. Every MCP tool says whether it changes anything (read-only, idempotent or destructive annotations), and the log tools and instructions tell agents that log output is data, not instructions.
- **Documentation:** a Trust section in the security docs (what Portside can do as you, what agents can do, what leaves the machine, the missing notarization and how to verify a download), and a comparison with process-compose, pitchfork, Portless, WhatThePort and Overmind.

## 0.2.1

- **Security:** builds use Go 1.27.3. Releases up to 0.2.0 were built with Go 1.26.0, without the Go security fixes since, including the one for distrusting compromised intermediate certificates in `crypto/x509` (GO-2026-6809), which matters for TLS connections such as remote Docker engines and paired computers.

## 0.2.0

- **Getting started:** the empty dashboard, which the macOS app opens on its first launch, connects the coding agents (MCP server and skill) next to finding projects. Settings has the same under **Coding agents**, also opened from the app's **Connect Coding Agents** and the command palette; the app's separate agent window is gone.
- **Agent prompt:** `portside setup --print-prompt`, and **Copy prompt for your agent** in the dashboard, give a request to paste into a coding agent so it connects the agents, adds your projects and checks that they start.
- **Tailscale help:** the **Computers** window says whether Tailscale is not installed (with a download link and the Homebrew command), not connected, or waiting for a sign-in, instead of one general message. **Copy prompt for your agent** there, and `portside computers --print-prompt`, give a request that has a coding agent set up Tailscale, sharing and pairing with you.
- **Coding agents:** setup connects Claude Code and Codex only; Gemini CLI is no longer set up or checked (an MCP server added earlier stays in its config, and `portside setup --print-mcp` still prints the entry for any client).
- **macOS app:** **Connect Coding Agents** asks to disconnect first while the dashboard shows another computer, where the setup is not shown.
- **Homebrew:** `brew uninstall --zap` stops only the daemon that holds the state directory's lock, instead of every process named `portside` (such as coding agents' MCP servers or an unrelated program of that name).
- **Documentation:** a short README, with the details in `docs/` by topic; the release archives include `docs/`.

## 0.1.0

First public release.

- **Servers:** a daemon runs dev servers in pseudo-terminals with dependencies, groups, restart policies and graceful process-group shutdown. Readiness is checked continuously (listener and health path), servers keep what you asked for across crashes and restarts, scrollback survives daemon restarts, and the daemon keeps running on the last good config when the file breaks. `daemon restart` upgrades the daemon in place without stopping servers. When port 7878 is taken by another program and the config sets no port, the daemon takes the first free port of 7879-7899, then 17879-17899, and keeps it in the state directory (`auto-port`) so it stays the same; it refuses to move when another of your daemons holds the port.
- **Environment:** servers get the login shell's environment and start inside their project's tool environment (direnv, devbox, mise, fnm or nvm, detected from the server's directory); the `wrap` key turns this off or sets a prefix such as `nix develop -c`.
- **Worktrees:** a configured server runs as a temporary copy (`<name>--<worktree>`) from another git checkout, on its own port, and goes away with the worktree.
- **Discovery:** `scan` and the dashboard propose servers for Node frameworks and workspaces, Python (uv, Poetry, Pipenv, PDM or a virtualenv), Laravel, Phoenix, Spring Boot, ASP.NET Core, Go, Rust, Deno and Compose, with a port only for commands known to take one. The starter config's root is the projects folder that holds the most projects.
- **Dashboard:** cards and a list view, live terminals with search across all kept output, CPU and memory history, ports, a config editor that keeps comments and a command palette. Open in editor and Open in Terminal use the installed apps, or the ones picked in Settings. Six theme families in light and dark; English and Polish.
- **Containers:** the Docker Engine behind the active context (Docker Desktop, OrbStack, Colima, Rancher Desktop, Linux, rootless Docker, Podman, and remote `tcp://` and `ssh://` contexts) is followed live. Compose containers attach to their servers; logs, a shell, start, stop, restart, recreate and rebuild per container, stack controls per project, warnings and notifications when a container is unhealthy or fails, and Start Docker when the engine is down.
- **Other computers:** share a daemon with your other devices over Tailscale (paired with one-time codes, every request signed) and manage that computer's servers from this one, with their ports forwarded here.
- **Coding agents:** an MCP server (`portside mcp`) for Claude Code, Codex and Gemini CLI, with `setup --print-mcp` printing the entry for other MCP clients, and a skill for Claude Code and Codex: list, ensure, restart and stop servers, read and search server and container logs with times, act on containers. `portside setup` and `doctor --fix` add what is missing; the macOS app offers the same in its Connect Coding Agents window on first launch.
- **CLI:** `up`, `down`, `restart`, `ensure`, `logs` (with `--grep`, `-C`, `--since`), `attach`, `containers`, `scan`, `add`, `share`, `computers`, `setup`, `skill`, `doctor` and more. `portside open` honors `$BROWSER`, works under WSL, and over SSH or without a browser prints the sign-in link.
- **`*.localhost` proxy** with WebSocket support.
- **macOS menu bar app** (macOS 26 or later, Apple Silicon): status icon, project icons, quick actions, crash and container notifications, a dashboard window and launch at login. Linux is a preview: the CLI, daemon and dashboard in the browser run there with a systemd user service, but are less tested.
- **Security:** loopback only, Host and Origin checks, a per-user API token, one-time sign-in links for browsers and sandboxed project icons.
