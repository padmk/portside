# Changelog

## 0.1.0

First public release.

- **Servers:** a daemon runs dev servers in pseudo-terminals with dependencies, groups, restart policies and graceful process-group shutdown. Readiness is checked continuously (listener and health path), servers keep what you asked for across crashes and restarts, scrollback survives daemon restarts, and the daemon keeps running on the last good config when the file breaks. `daemon restart` upgrades the daemon in place without stopping servers. When port 7878 is taken by another program, the daemon picks a free port and saves it in the config.
- **Environment:** servers get the login shell's environment and start inside their project's tool environment (direnv, devbox, mise, fnm or nvm, detected from the server's directory); the `wrap` key turns this off or sets a prefix such as `nix develop -c`.
- **Worktrees:** a configured server runs as a temporary copy (`<name>--<worktree>`) from another git checkout, on its own port, and goes away with the worktree.
- **Discovery:** `scan` and the dashboard propose servers for Node frameworks and workspaces, Python (uv, Poetry, Pipenv, PDM or a virtualenv), Laravel, Phoenix, Spring Boot, ASP.NET Core, Go, Rust, Deno and Compose, with a port only for commands known to take one. The starter config's root is the projects folder that holds the most projects.
- **Dashboard:** cards and a list view, live terminals with search across all kept output, CPU and memory history, ports, a config editor that keeps comments and a command palette. Open in editor and Open in Terminal use the installed apps, or the ones picked in Settings. Six theme families in light and dark; English and Polish.
- **Containers:** the Docker Engine behind the active context (Docker Desktop, OrbStack, Colima, Rancher Desktop, Linux, rootless Docker, Podman, and remote `tcp://` and `ssh://` contexts) is followed live. Compose containers attach to their servers; logs, a shell, start, stop, restart, recreate and rebuild per container, stack controls per project, warnings and notifications when a container is unhealthy or fails, and Start Docker when the engine is down.
- **Other computers:** share a daemon with your other devices over Tailscale (paired with one-time codes, every request signed) and manage that computer's servers from this one, with their ports forwarded here.
- **Coding agents:** an MCP server (`portside mcp`) for Claude Code, Codex and Gemini CLI, with `setup --print-mcp` printing the entry for other MCP clients, and a skill for Claude Code and Codex: list, ensure, restart and stop servers, read and search server and container logs with times, act on containers. `portside setup` and `doctor --fix` add what is missing; the macOS app offers the same in its Connect Coding Agents window on first launch.
- **CLI:** `up`, `down`, `restart`, `ensure`, `logs` (with `--grep`, `-C`, `--since`), `attach`, `containers`, `scan`, `add`, `share`, `computers`, `setup`, `skill`, `doctor` and more. `portside open` honors `$BROWSER`, works under WSL, and over SSH or without a browser prints the sign-in link.
- **`*.localhost` proxy** with WebSocket support.
- **macOS menu bar app** (macOS 26 or later, Apple Silicon): status icon, project icons, quick actions, crash and container notifications, a dashboard window and launch at login. Linux uses the dashboard in the browser and a systemd user service.
- **Security:** loopback only, Host and Origin checks, a per-user API token, one-time sign-in links for browsers and sandboxed project icons.
