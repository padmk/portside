# Compared with other tools

Several tools cover part of what Portside does, and for some setups one of them fits better. This page was checked against each project's own repository and site in October 2026; their features change, so check them before you decide.

| | Supervises servers (restart policies, readiness) | GUI | MCP for coding agents | Docker containers | Git worktrees | Platforms |
| --- | --- | --- | --- | --- | --- | --- |
| **Portside** | Yes, in terminals (PTYs), with tasks, schedules, file watching and memory limits | macOS menu bar app, web dashboard | Yes: start or reuse, restart, stop, logs, containers | Compose containers shown with their server: logs, shell, recreate | Temporary copy of a server per worktree, own port | macOS 26+ on Apple Silicon; Linux (x86-64, arm64) |
| [process-compose](https://github.com/F1bonacc1/process-compose) | Yes: dependencies, health checks, replicas, schedules | TUI, REST API | Yes | No | No | macOS, Linux, Windows |
| [pitchfork](https://github.com/jdx/pitchfork) | Yes: ready checks, retries, one-shot tasks, file watching, cron schedules, memory limits, shell hooks | TUI, web UI | Yes | Not checked | Per-worktree `.localhost` names | macOS, Linux, Windows |
| [Portless](https://github.com/vercel-labs/portless) | No (wraps a command you run) | No | Lists routes and manages aliases; does not start servers | Port aliases only | Per-worktree `.localhost` names | macOS, Linux, Windows |
| [WhatThePort](https://github.com/tomjohndesign/what-the-port) | No (finds running servers, can stop or rerun them) | macOS menu bar app, TUI | No (JSON list for scripts) | No | Cleans up servers of deleted worktrees | macOS 14+ on Apple Silicon |
| [Overmind](https://github.com/DarthSim/overmind) | Partly (restarts processes that die) | tmux | No | No | No | macOS, Linux, BSD |

## When another tool fits better

- **process-compose** for a stack defined in the repository and run the same way on every platform, including Windows and CI. It is several years old and widely used. Use 1.120.0 or later if you expose its MCP server over SSE ([GHSA-5gm3-9crp-6g3v](https://github.com/F1bonacc1/process-compose/security/advisories/GHSA-5gm3-9crp-6g3v)).
- **pitchfork** for per-project TOML with shell hooks that start daemons when you enter a project, Windows support, or if you already use mise. It is the closest to Portside in scope: both run one-shot tasks before the services that depend on them, restart on file changes, run on schedules and limit memory. pitchfork's watch patterns are relative to the project directory of its config file and its cron expressions start with a seconds field; Portside's patterns are relative to each server's directory and its schedules use the five crontab fields.
- **Portless** for named `.localhost` URLs with trusted HTTPS (cookies, OAuth callbacks, HTTP/2), LAN access or public tunnels, while you keep running servers in your own terminals.
- **WhatThePort** to see and clean up what is already running, including which agent session started it, without changing how servers are launched. It also runs on older macOS versions.
- **Overmind** for Procfile projects that want tmux.

## Where Portside differs

- One supervisor for every project on the machine, with a menu bar app, notifications and a dashboard, instead of one configuration per repository.
- Compose containers belong to the server that starts them, with their state, health, logs, a shell and recreate next to it.
- `ensure_server` lets an agent reuse a running server or start it and get its URL, and starts a temporary copy from the agent's worktree on its own port.
- Pairing with your other computers over Tailscale, to drive their servers and open their sites locally.

## Where it is weaker

- It is new: the first public release was in October 2026, and it has few users so far.
- The menu bar app needs macOS 26 on Apple Silicon. On Linux there are the CLI, the dashboard and the MCP server without it, and there is no Windows version.
- The app is not notarized by Apple (see [Security](security.md#trust)).
- `*.localhost` addresses are plain HTTP.
