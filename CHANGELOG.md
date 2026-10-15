# Changelog

## 0.1.0

First release.

- Daemon that runs dev servers in pseudo-terminals with dependencies, groups, restart policies, graceful process-group shutdown and restore after restarts or login.
- Dashboard with adaptive cards and a list view, live terminals, CPU and memory history, a ports overview, project discovery, a config editor and a command palette. Six theme families in light and dark; English and Polish.
- `*.localhost` proxy with WebSocket support.
- CLI (`up`, `down`, `restart`, `ensure`, `logs -f`, `attach`, `scan`, `import devctl`, `doctor` and more) and an MCP server for coding agents.
- macOS menu bar app with status icon, quick actions, crash notifications with Restart and Show Logs, a dashboard window and launch at login. Linux uses the dashboard in the browser and a systemd user service.
- Security: loopback only, Host and Origin checks, a per-user API token, one-time sign-in links for browsers, sandboxed project icons.
