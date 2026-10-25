# Changelog

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
