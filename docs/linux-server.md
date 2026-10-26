# Linux server without a desktop

Portside runs on a Linux machine you reach over SSH, such as a development or staging server: the daemon as a systemd user service, the CLI and coding agents through MCP, the dashboard through an SSH tunnel. Linux is a preview: CI builds and tests it, but macOS gets more use. Portside is a tool for development servers, not a production supervisor.

## Install and start at boot

Download `portside_<version>_linux_<arch>.tar.gz` (x86-64 or arm64) from the [releases](https://github.com/padmk/portside/releases) and put `portside` on your `PATH` (for example `~/.local/bin`). With Homebrew, `brew install --formula padmk/tap/portside` builds it from source instead (a few minutes), and `brew upgrade portside` updates it. Then:

```sh
portside init                       # writes ~/.config/portside/portside.toml
sudo loginctl enable-linger $USER   # keep user services running without a login
portside service install            # start the daemon now and at every boot
portside doctor                     # check the setup
```

Portside needs `ps`, `tail` and either `lsof` or `ss` (iproute2, installed on most distributions). Desktop notifications need `notify-send` and a desktop session; without one, use `on_crash` instead (below). Docker or Podman is optional too; for rootless Docker, set a Docker context or `DOCKER_HOST` in your login shell ([Containers](containers.md)). How the service runs without a login session is in [How it works](how-it-works.md#login-service).

After an update, run `portside daemon restart` with the new binary: servers keep running through it.

## Servers that stay up

Give each server a restart policy, `health` when it serves HTTP, and `ready_log` when it only becomes ready after it prints something:

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
port = 0                            # no port
ready_log = "Worker ready"          # running once it prints this line
restart = "always"
```

A crashed server is started again with backoff until it has failed 5 times in a row ([Server keys](configuration.md#server-keys) has the delays), and running servers come back after a reboot (`restore`, on by default). systemd restarts the daemon itself if it crashes, so do not also run these apps as systemd units of their own.

## Crash reports

Desktop notifications need a desktop. On a server, set `on_crash`: it runs your command once a server has crashed for good, with the server, exit code, note and last log lines in `PORTSIDE_*` variables ([Crash command](configuration.md#crash-command)). When the command itself fails, `portside daemon logs` says so.

A crash of the daemon itself is systemd's to report: `systemctl --user status portside`. While systemd runs the daemon, its output is in the user journal, which `portside daemon logs` reads then.

## Reading state from scripts

- `portside check` answers whether everything that should run is running, for monitors and timers, without the token. A server should run when it crashed, when it exited although its restart policy is `always`, and while it is started and not stopped by you; it is ready when it is `running`. It exits 0 when all such servers are ready, 1 when one is not (printing each with its status and reason) and 2 when it cannot tell: no daemon runs (it never starts one), what answers cannot prove it is yours, or the config does not load. `--json` prints `ok`, the daemon's `version` and `pid`, and per server `name`, `status`, `expected`, `ready` and `reason`, or `ok: false` with `error` for exit 2. In a crontab: `*/5 * * * * portside check >/dev/null || mail -s "dev servers" you@example.com`.
- `portside status --json`: every server with `status` (`starting`, `running`, `stopping`, `stopped`, `crashed`, `exited`, or `blocked` when another process holds its port), `note`, `exitCode`, `restarts`, `restartPending`, `pid`, `port`, `url` and the last lines in `tail`. Fields without a value, such as `exitCode` of a running server, are left out.
- `portside logs <name> -n 200` prints a server's output (`--grep`, `--since 10m`, `-t` for times) and `portside daemon logs -n 100` the daemon's; `-f` follows either.
- `portside ensure <name> --json` starts a server if needed and waits until it is ready.
- The HTTP API on `127.0.0.1` accepts `Authorization: Bearer $(cat ~/.local/state/portside/auth-token)`, for example `curl -H "Authorization: Bearer $(cat ~/.local/state/portside/auth-token)" http://127.0.0.1:7878/api/state`. The token controls every server, so keep it on the machine; give other systems only what you read from it.

`portside status` exits 0 whatever state the servers are in; use `portside check` for an exit code.

## Coding agents

`portside setup` registers the MCP server and the skill with Claude Code and Codex, through their own CLIs, as on a Mac ([Coding agents](coding-agents.md)). The agent then reuses running servers and reads their logs. For an agent that should only look, register it with `portside setup --mcp-mode read-only`; to limit it to one project, add `--dir` to its registration ([Modes and project scope](coding-agents.md#modes-and-project-scope)).

## The dashboard over SSH

```sh
ssh -L 7878:127.0.0.1:7878 <server>   # on your computer
portside open                         # on the server: prints a sign-in link
```

Open the link on your computer ([Opening the dashboard](cli.md#opening-the-dashboard) has the details). To reach the dashboard from your own computers without a tunnel, see [Other computers](other-computers.md).
