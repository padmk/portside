---
name: {{.ID}}
description: "Find, start, reuse, debug or restart this machine's local dev servers and their Docker containers through {{.Name}}. Use before running a dev server, preview or `docker compose up`, when a local URL or port is needed, or when a local server or its database misbehaves."
---

# {{.Name}}

{{.Name}} runs this machine's dev servers in a daemon on `http://127.0.0.1:{{.Port}}`: dashboard, API, and a proxy that serves each server at `http://<name>.localhost:{{.Port}}`. The roster is `{{.Config}}`, the CLI is `{{.ID}}`, and the MCP server `{{.ID}}` (`{{.ID}} mcp`) offers the same as tools. Use the tools when they are loaded, the CLI otherwise. Repository run instructions take precedence: a documented Compose or container gateway stays authoritative even if the roster has a similarly named entry.

## Get a server running

Check what exists before starting anything: `{{.ID}} status --json` or `list_servers`. Reuse a running server and the URL it reports. To get one running and ready, use `{{.ID}} ensure <name> --json` or `ensure_server`: it starts the server and its dependencies only if needed, waits until the port accepts connections and returns the URL, so it is safe to repeat and replaces starting and polling yourself.

A server runs one checkout. In a worktree or another clone, compare the server's `dir` with your working directory before reusing it (`list_servers` with `dir` does this): a server running the main checkout does not show your changes. Ask before pointing it elsewhere or adding an entry for the worktree.

Never run a second copy of a managed server in your own shell, not even on another port. A server shown as `external` (or `blocked`) has its port held by a process {{.Name}} did not start: find the owner with `{{.ID}} ports` or `list_ports` and leave it alone unless the user asks; `{{.ID}} up <name> --takeover` replaces it.

## Diagnose

Read the output before any restart: `{{.ID}} logs <name> -n 200`, or `server_logs`. To search the whole scrollback, pass a case-insensitive regular expression, with context lines for stack traces: `{{.ID}} logs <name> --grep 'error|exception' -C 5`, or `match` and `context` in `server_logs`. The tools put the local time before each line and the current time in a note at the top; check it before blaming a line on your change. To see only what happened after a change or a request, pass `since` (`10m`, or an RFC 3339 time), or `{{.ID}} logs <name> -t --since 10m`. Bracketed lines at the top of a reply say what it left out.

Each server lists the containers of the Compose project in its directory (state, health, the engine's status text, `failed`, `projectDir`); `list_containers` or `{{.ID}} containers` shows every container on the engine. When a server misbehaves, check them: a container that is unhealthy, restarting or `failed` is a lead to follow, not yet the cause. Read its output with `container_logs` (same `lines`, `match`, `context` and `since`) or `{{.ID}} logs <container>`. `list_ports` names the container behind a published port. If the `docker` field says the engine is unavailable, tell the user: the dashboard's **Start Docker** button starts the engine behind the active Docker context.

## Change

Restart only the named server, for a diagnosed failure or a change hot reload cannot pick up, and ask first unless the user requested it. A note saying the config changed since start means a restart applies it. `down`, `stop` and `restart` need explicit names; do not use `--all` unless asked, never stop the daemon (`{{.ID}} daemon stop`), and use `logs` rather than the interactive `attach`.

Manage containers with the `docker` CLI in the container's `projectDir`, with the same compose files and env files the project's own scripts use. Rebuild one service after changing its Dockerfile or dependencies with `docker compose up -d --build --no-deps <service>`; without `--no-deps` Compose may also recreate the services it depends on. Never remove volumes (`docker compose down -v`, `docker volume rm`) without the user's confirmation: they hold the databases.

## Outside the roster

For a project the roster does not have, run a task-owned preview through the normal execution tool and stop only that process. Add a persistent entry only when the user asks or the repository workflow establishes one: `{{.ID}} add <name> --dir <dir> --cmd "<command with {PORT}>" --port <port> [--group <group>]`. Pick an explicit port that `{{.ID}} ports` shows as free. The daemon reloads the file on its own, and `{{.ID}} config check` validates hand edits.

Report the URL and whether the server was reused, started or restarted.

<!-- Installed by `{{.ID}} skill install`; {{.Name}} replaces this file when it updates. -->
