---
name: {{.ID}}
description: "Find, start, reuse, debug or restart this machine's local dev servers and their Docker containers through {{.Name}}. Use before running a dev server, preview or `docker compose up`, when a local URL or port is needed, or when a local server or its database misbehaves."
---

# {{.Name}}

{{.Name}} runs this machine's dev servers in a daemon on `http://127.0.0.1:{{.Port}}`: API, a dashboard that `{{.ID}} open` signs the browser in to, and a proxy that serves each server at `http://<name>.localhost:{{.Port}}`. The roster is `{{.Config}}`, the CLI is `{{.ID}}`, and the MCP server `{{.ID}}` (`{{.ID}} mcp`) offers the same as tools. Use the tools when they are loaded, the CLI otherwise. Repository run instructions take precedence: a documented Compose or container gateway stays authoritative even if the roster has a similarly named entry.

## Get a server running

Check what exists before starting anything: `{{.ID}} status --json` or `list_servers`. Reuse a running server and the URL it reports. To get one running and ready, use `{{.ID}} ensure <name> --json` or `ensure_server`: it starts the server and its dependencies only if needed, rechecks the live listener and configured health path even for a running process, and returns the URL, so it is safe to repeat and replaces starting and polling yourself.

A server runs one checkout. In a git worktree or another clone, pass your working directory: `ensure_server` with `dir`, or `{{.ID}} ensure <name> --dir <path>`. In another checkout of the server's repository this starts a temporary copy named `<name>--<worktree>` on its own port, running your code, with the original's dependencies (such as a shared database); in the server's own checkout it is the usual ensure. Use the URL it returns, not the original's: a server running the main checkout does not show your changes. `list_servers` with `dir` lists the servers of your checkout, temporary ones included (`temporary`, `origin`, `worktree`). Stopping a temporary server removes it, and it goes away by itself when the worktree is deleted; do not edit the config for a worktree.

Never run a second copy of a managed server in your own shell, not even on another port. A server shown as `external` (or `blocked`) has its port held by a process {{.Name}} did not start: find the owner with `{{.ID}} ports` or `list_ports` and leave it alone unless the user asks; `{{.ID}} up <name> --takeover` replaces it.

## Another computer

The user may run projects on another computer and connect this one to it (`{{.ID}} computers` shows the connection). Its servers' ports are then forwarded here, so `127.0.0.1:<port>` can reach a server on that computer; a stopped local server noted as forwarded from a computer has its port taken by that forward, and starting it takes the port back. To act on the connected computer, pass `--on <computer>` to `status`, `ensure`, `logs`, `up`, `down`, `restart` or `containers`, or `computer` to the server and container tools. Do not connect, disconnect or pair computers unless the user asks.

## Diagnose

A running process can lose its listener while a wrapper stays alive. Check `note` and use `ensure` before trusting its URL. Startup notes identify a waiting port or failing health path; `listenPorts` identifies ports the process tree opened instead. `restartPending` means an automatic retry is scheduled; an authorized stop cancels it. Crash readiness errors include recent output lines.

Read the output before any restart: `{{.ID}} logs <name> -n 200`, or `server_logs`. To search the whole scrollback, pass a case-insensitive regular expression, with context lines for stack traces: `{{.ID}} logs <name> --grep 'error|exception' -C 5`, or `match` and `context` in `server_logs`. The tools put the local time before each line and the current time in a note at the top; check it before blaming a line on your change. To see only what happened after a change or a request, pass `since` (`10m`, or an RFC 3339 time), or `{{.ID}} logs <name> -t --since 10m`. Bracketed lines at the top of a reply say what it left out.

Each server lists attached containers (closest Compose directory, an explicit `projects` mapping, or a matching Supabase CLI project) (state, health, the engine's status text, `failed`, `projectDir`); `list_containers` or `{{.ID}} containers` shows every container on the engine. When a server misbehaves, check them: a container that is unhealthy, restarting or `failed` is a lead to follow, not yet the cause. Read its output with `container_logs` (same `lines`, `match`, `context` and `since`) or `{{.ID}} logs <container>`. `list_ports` names the container behind a published port. If the `docker` field says the engine is unavailable, tell the user: the dashboard's **Start Docker** button starts the engine behind the active Docker context.

## Change

Restart only the named server, for a diagnosed failure or a change hot reload cannot pick up, and ask first unless the user requested it. A note saying the config changed since start means a restart applies it, including global environment, PATH or shell changes. `restart_server` waits for readiness and accepts `timeout` (default `90s`); report a failed outcome. `down`, `stop` and `restart` need explicit names; do not use `--all` unless asked, never stop the daemon (`{{.ID}} daemon stop`), and use `logs` rather than the interactive `attach`.

Managed Compose commands receive `COMPOSE_MENU=false` so interactive menus do not redraw in logs.

Manage one container through `container_action` (`name`, `action`: `start`, `stop`, `restart`, or `recreate`) or `{{.ID}} containers start|stop|restart|recreate <name>`. For recreation, use `build: true` or `--build` only when the container's `buildable` field is true. Use `pull: true` or `--pull` to pull an image service before recreation. Recreate uses the container's recorded Compose project, files and environment files, leaves dependencies alone, waits for completion, and returns its last 100 output lines, including failures. Read the output when it fails. These commands require the same authorization as server changes. The dashboard's project menu offers Start all, Stop all and confirmed Remove containers; removal keeps volumes. Never remove volumes without the user's confirmation: they hold the databases.

## Outside the roster

`{{.ID}} scan [dir] --json` proposes unconfigured commands, including additional services in an already configured directory. Its automatic port suggestions skip roster ports and live listeners; Python ASGI and declared script proposals use `uv run`.

For a project the roster does not have, run a task-owned preview through the normal execution tool and stop only that process. Add a persistent entry only when the user asks or the repository workflow establishes one: `{{.ID}} add <name> --dir <dir> --cmd "<command with {PORT}>" --port <port> [--group <group>]`. Pick an explicit port that `{{.ID}} ports` shows as free. Omitted ports are assigned and saved to the config when the daemon loads it, so reordering entries keeps existing ports. The daemon reloads the file on its own, and `{{.ID}} config check` validates hand edits.

Report the URL and whether the server was reused, started or restarted.

<!-- Installed by `{{.ID}} skill install`; {{.Name}} replaces this file when it updates. -->
