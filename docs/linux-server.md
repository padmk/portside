# Linux server without a desktop

Portside runs on a Linux machine you reach over SSH, such as a development or staging server: the daemon as a systemd user service, the CLI and coding agents through MCP, the dashboard through an SSH tunnel. Linux is a preview: CI builds and tests it, but macOS gets more use. Portside is a tool for development servers, not a production supervisor.

## Install and start at boot

Download `portside_<version>_linux_<arch>.tar.gz` (x86-64 or arm64) from the [releases](https://github.com/padmk/portside/releases), put `portside` on your `PATH` (for example `~/.local/bin`), then:

```sh
portside init                       # writes ~/.config/portside/portside.toml
sudo loginctl enable-linger $USER   # keep user services running without a login
portside service install            # start the daemon now and at every boot
portside doctor                     # check the setup
```

Portside needs `ps`, `tail` and either `lsof` or `ss` (iproute2, installed on most distributions). `notify-send` is optional; without a desktop, use `on_crash` instead (below). Docker or Podman is optional too; for rootless Docker, a Docker context or `DOCKER_HOST` set in your login shell picks it ([Containers](containers.md)).

After an update, run `portside daemon restart` with the new binary: servers keep running through it.

## Servers that stay up

Give each server a restart policy, and `health` when it serves HTTP:

```toml
on_crash = "~/bin/report-crash"     # see below

[servers.api]
dir = "apps/api"
cmd = "uv run uvicorn api.main:app --port {PORT}"
port = 8100
health = "/healthz"
restart = "on-failure"

[servers.worker]
dir = "apps/worker"
cmd = "uv run python -m worker"
port = 0                            # no port: running 1.5 s after it starts
restart = "always"
```

With `on-failure` a crashed server is started again after 1, 2, 4, 8 and 16 seconds; after 5 restarts in a row it stays `crashed` ([Server keys](configuration.md#server-keys)). Running servers come back after a reboot (`restore`, on by default). systemd restarts the daemon if it crashes; do not also run the same apps as their own systemd units.

## Crash reports

Notifications go to a desktop, so on a server they go nowhere. `on_crash` runs a command once a server has crashed for good, with the server, exit code, note and last log lines in `PORTSIDE_*` variables ([Crash command](configuration.md#crash-command)). A failed run of the command is in `portside daemon logs`.

The daemon's own crashes are systemd's: `systemctl --user status portside` and `journalctl --user -u portside`.

## Reading state from scripts

- `portside status --json`: every server with `status` (`starting`, `running`, `stopping`, `stopped`, `crashed`, `exited`, or `blocked` when another process holds its port), `note`, `exitCode`, `restarts`, `restartPending`, `pid`, `port`, `url` and the last lines in `tail`. Fields may be added; existing ones keep their meaning.
- `portside logs <name> -n 200` (`--grep`, `--since 10m`, `-t` for times), `portside daemon logs -n 100`; `-f` follows either.
- `portside ensure <name> --json` starts a server if needed and waits until it is ready.
- The HTTP API on `127.0.0.1` accepts `Authorization: Bearer $(cat ~/.local/state/portside/auth-token)`, for example `curl -H "Authorization: Bearer $(cat ~/.local/state/portside/auth-token)" http://127.0.0.1:7878/api/state`. The token controls every server, so keep it on the machine; give other systems only what you read from it.

`portside status` exits 0 whatever the servers' states; read `status` from the JSON instead.

## Coding agents

`portside setup` registers the MCP server and the skill with Claude Code and Codex, through their own CLIs, as on a Mac ([Coding agents](coding-agents.md)). The agent then reuses running servers and reads their logs. `portside setup --mcp-mode read-only` registers it for an agent that should only look, and `--dir <project>` limits it to one project ([Modes and project scope](coding-agents.md#modes-and-project-scope)).

## The dashboard over SSH

Forward the daemon's port and sign in with a link printed on the server:

```sh
ssh -L 7878:127.0.0.1:7878 <server>   # on your computer
portside open --print                 # on the server, in that session
```

Open the printed link on your computer; it works once, within two minutes, and signs you in at the dashboard's `*.localhost` address. When port 7878 is taken on your computer, forward another one (`-L 17878:127.0.0.1:7878`) and change the port in the link to match. To use the dashboard from your own computers without a tunnel, see [Other computers](other-computers.md).
