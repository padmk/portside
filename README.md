<p align="center">
  <img src="design/brand/tug-256.png" width="128" alt="">
</p>

<h1 align="center">portside</h1>

<p align="center">Keep your local dev servers running, in one place.</p>

<p align="center">
  <img src="design/screenshots/overview-dark.png" width="900" alt="Portside dashboard in Rosé Pine: server cards grouped by project, with running, stopped and crashed servers">
</p>

Portside is a small daemon, a CLI and a web dashboard for the dev servers you run every day: Next.js, Vite, Python APIs, `docker compose` stacks, workers. It starts them in real pseudo-terminals, keeps their ports stable, shows status, logs and resource use on one calm page, and lets coding agents reuse the same servers instead of launching duplicates.

On macOS it is a menu bar app; on Linux the same dashboard runs in the browser. One static binary inside, no runtime dependencies, no account, nothing leaves `127.0.0.1`.

## Features

- **A menu bar app on macOS.** The status icon shows at a glance whether everything runs, something is starting, or a server or one of its containers needs attention. Its panel lists every server with start, stop, restart, open and logs, plus group actions; the dashboard opens in its own window. Crashes arrive as notifications with **Restart** and **Show Logs** buttons.

- **One list for every project.** A single TOML file describes servers across all your repositories, with groups (`portside up shop` starts web, api and worker) and dependencies (`api` waits for `db` to accept connections).
- **Stable ports and clean URLs.** Each server keeps its port. Every server is also reachable at `http://<name>.localhost:7878` through a built-in proxy that handles WebSockets and hot reload.
- **Real terminals.** Servers run in a PTY, so colors, progress bars and interactive prompts work. Watch output live in the dashboard or attach from your terminal (`portside attach web`), type into it, detach with `Ctrl-]`.
- **Honest status.** `starting` until the port accepts connections (optionally an HTTP health path), `running`, `crashed` with the exit code, `blocked` when another process holds the port. Ports held by programs Portside did not start are shown with their owner, and you can take the port over in one click.
- **Graceful stops.** `Ctrl-C` to the whole process group first (so `docker compose` shuts its containers down), then `SIGTERM`, then `SIGKILL`; orphaned children are cleaned up.
- **Restart policies and notifications.** `on-failure` or `always` with exponential backoff; a desktop notification when a server crashes.
- **Survives restarts.** The servers you had running come back when the daemon restarts or you log in (`portside service install`).
- **CPU and memory** per server, summed over the whole process tree, with sparklines.
- **A dashboard that scales with your setup.** One server fills the screen with its output and charts, a few get large cards with a live log preview, and long lists switch to compact rows (`c` and `l` toggle them). Click a card for a full-size terminal, with every server one click away in a side rail. Everything else (ports, config, add server) stays in the menu and the `⌘K` palette; `?` lists the keyboard shortcuts.
- **Themes.** Rosé Pine (default), Everforest, Tokyo Night, Catppuccin, Nord and a neutral palette, each in light and dark, following the system setting or your choice. The terminal follows the theme.
- **Project discovery.** `portside scan` finds Next.js, Vite, Astro, Nuxt, Remix, Django, Rails, Python ASGI apps and Compose projects and proposes commands with the right port flags. Python apps and declared `[project.scripts]` entry points use `uv run`; entry points have no assumed listening port. Discovery skips commands already configured, keeps other services in the same folder, and checks live listeners when suggesting automatic ports.
- **Agent friendly.** `portside ensure web` starts a server if needed, waits until it is ready and prints its URL. `portside mcp` exposes servers, containers and searchable logs to Claude Code, Codex and other MCP clients, and `portside skill install` teaches agents how to use them.
- **Containers.** Portside talks to the Docker Engine API of the active Docker context, so it works the same with OrbStack, Docker Desktop, Colima, Rancher Desktop or a Linux daemon. Compose containers appear next to the server whose directory is their project directory, including ones that project scripts start: state, health check, uptime, published ports, CPU and memory, updated live. Servers that share a directory share its containers. A server warns when one of its containers is unhealthy, restarting or crashed while it runs; a container stopped on purpose (a stop signal from `docker stop`, `compose stop` or `docker kill`, or an exit the engine did not restart under an `always` or `unless-stopped` policy without an error) does not count as crashed. Each container has start, stop and restart, its logs in a searchable terminal, a shell (bash or sh) in a terminal tab, details (image, command, environment with secret-looking values hidden until revealed, volumes, networks) and, for Compose services, **Recreate** and **Rebuild and recreate**. A **Containers** page (`g d`) shows every container on the engine with the same actions. When the engine is down, **Start Docker** starts the one behind the active context, or the `docker_start` command.
- **Other computers.** With Tailscale on both machines, the dashboard connects to Portside on another of your computers and manages its servers, containers, logs and terminals as if you sat at it. Its sites open in your local browser: running servers' ports are forwarded to the same ports here, and every server is also at `http://<name>.<computer>.localhost:7878`. See [Other computers](#other-computers).
- **Ports overview.** Every listening TCP port on the machine with its process and working directory, the container a published port belongs to, and a safe way to kill strays.

<table>
  <tr>
    <td><img src="design/screenshots/detail.png" alt="A server's live terminal with every server one click away in the side rail (Tokyo Night)"></td>
    <td><img src="design/screenshots/crashed.png" alt="A crashed server with its exit code and the error that caused it (Catppuccin Latte)"></td>
  </tr>
  <tr>
    <td colspan="2"><img src="design/screenshots/list.png" alt="The list layout with every server grouped by project (Everforest Light)"></td>
  </tr>
  <tr>
    <td colspan="2"><img src="design/screenshots/containers.png" alt="A server's Compose containers with their state, usage and live logs (Nord)"></td>
  </tr>
</table>

## Install

Requirements: macOS 14 or later on Apple Silicon (M1 or newer); Linux on x86-64 or arm64.

macOS, with Homebrew (planned; the tap is not published yet). This will install `Portside.app` and the `portside` command:

```sh
brew install --cask padmk/tap/portside
```

Or download `Portside-<version>.zip` from the releases, move `Portside.app` to Applications, and choose **Install Command Line Tool** in its menu.

Linux: download `portside_<version>_linux_<arch>.tar.gz` from the releases, put `portside` on your `PATH`, and run `portside service install` to start the daemon at login.

From source (Go 1.26+, Node 24 and pnpm 12):

```sh
git clone https://github.com/padmk/portside
cd portside
make install            # builds the dashboard and installs ~/.local/bin/portside
make app-install        # macOS: builds Portside.app and installs it in /Applications
```

To update an installed app, run `make app-install` (it quits a running copy and reopens it) and then `portside daemon restart` so the daemon runs the new binary. Servers that were running are restored.

## Quick start

```sh
portside scan --add     # find projects under ~/dev and add them
portside up web --wait  # start one server and wait until it is ready
portside open           # sign in and open the dashboard (http://127.0.0.1:7878)
portside service install  # start the daemon at login (optional)
```

Any command starts the daemon on demand, so there is nothing to launch first.

## Configuration

The config lives at `~/.config/portside/portside.toml` (`$PORTSIDE_CONFIG` overrides it). The daemon reloads it when it changes; edits from the dashboard keep your comments and formatting. Adding or editing a server with a missing working directory saves it and displays a warning; create the directory before starting it.

```toml
root = "~/dev"                      # base for relative dirs
# port = 7878                       # dashboard, API and *.localhost proxy
# path = ["/opt/homebrew/opt/node@24/bin"]   # prepended to PATH for every server
# restore = true                    # bring back running servers after a restart
# notify = true                     # desktop notification on crash
# stop_timeout = "10s"              # grace period after Ctrl-C
# editor = "code"                   # used by "Open in editor"
# terminal = "Terminal"             # app (macOS) or command for "Open in Terminal"
# docker_start = "colima start"     # how "Start Docker" starts the engine; detected by default
# share = false                     # let your other devices on Tailscale connect (see Other computers)

[servers.shop-web]
dir = "shop/apps/web"
cmd = "pnpm dev --port {PORT}"
port = 3001
group = "shop"
depends_on = ["shop-api"]

[servers.shop-api]
dir = "shop/apps/api"
cmd = "uv run uvicorn api.main:app --reload --port {PORT}"
port = 3002
group = "shop"
health = "/healthz"
restart = "on-failure"

[servers.shop-worker]
dir = "shop"
cmd = "pnpm worker"
port = 0                            # listens on no port
group = "shop"
env = { QUEUE = "local" }

[servers.shop-db]
dir = "shop"
cmd = "docker compose up --menu=false"
port = 5432
group = "shop"
stop_timeout = "30s"
```

| Key | Meaning |
| --- | --- |
| `dir` | Working directory, relative to `root` or absolute. |
| `cmd` | Shell command, run with `bash -c`. `{PORT}` is replaced; `$PORT` is set too. |
| `port` | Port the server listens on. Omit it to assign a port from 3001, saved to the file on daemon load; `0` for none. |
| `group` | Name for starting and stopping related servers together. |
| `depends_on` | Servers that must be running (and ready) before this one starts. |
| `restart` | `no` (default), `on-failure` or `always`, with backoff up to 30 s and at most 5 quick retries. |
| `health` | HTTP path that must answer (status < 500) before the server counts as running. |
| `env` | Extra environment variables. `{PORT}` works in values. |
| `stop_timeout` | How long to wait after `Ctrl-C` before `SIGTERM`. |
| `enabled` | `false` leaves the server out of `--all` and restore; it can still be started by name. |
| `icon` | Path to an icon for the dashboard; by default a project favicon is detected. |
| `open` | Path appended to the URL when opening the server, e.g. `/admin`. |

`terminal` accepts a macOS app name or an executable with arguments. Commands receive the directory as their last argument; use `{DIR}` to place it explicitly, for example `terminal = "kitty --directory {DIR}"`. Without this setting, macOS opens Terminal and Linux uses the first available terminal emulator.

Server names are lowercase letters, digits and dashes, because they double as `<name>.localhost` hostnames.

The daemon is often started by launchd or systemd with a bare environment, so it reads `PATH` and the rest of your environment from your account's login shell (bash, zsh or fish; the one set for your user, not `$SHELL`) and refreshes it every 10 minutes. Commands detected as Compose stacks receive `COMPOSE_MENU=false` to keep interactive Compose menus out of their logs. Ports 5000 and 7000 (macOS AirPlay) and browser-blocked ports are skipped when assigning ports.

## CLI

```text
portside status [--json]                 list servers with state, port and usage
portside up <name|group>... | --all      start (dependencies first); --wait, --takeover
portside down <name|group>... | --all    stop gracefully
portside restart <name|group>...
portside ensure <name> [--json]          start if needed, wait until ready, print the URL
portside logs <name> [-n 200] [-f]       print or follow a server's or container's output;
                                         --grep <regexp> [-C N] searches it,
                                         -t adds times, --since 10m limits it
portside containers [server] [--json]    containers, their state and server
portside attach <name>                   interactive terminal, Ctrl-] detaches
portside open [name] [--alias]           open the dashboard or a server
portside ports [--json]                  every listening TCP port and its owner

portside add <name> --cmd "..." [--dir .] [--port N] [--group G]
portside rm <name> | enable <name> | disable <name>
portside scan [dir] [--add]              discover projects
portside import devctl [servers.conf]    import a devctl roster, keeping ports
portside config [path|edit|check]

portside daemon [start|stop|restart|status|logs]
portside service install|uninstall|status
portside share [on|off]                  let your paired devices on Tailscale connect;
                                         on prints a pairing code
portside share code|devices|revoke <device>   new pairing code, paired devices, unpair one
portside mcp                             MCP server on stdio
portside skill install|print             agent skill for Claude Code and Codex
portside doctor                          check the setup
```

`down` and `stop` refuse to run without a name or `--all`.

## Coding agents

Register the MCP server once:

```sh
claude mcp add --scope user portside -- portside mcp
codex mcp add portside -- portside mcp
```

Agents get `list_servers` (each server with the containers of its Compose project; `dir` narrows it to one checkout), `ensure_server` (reports whether it reused or started the server), `restart_server`, `stop_server`, `server_logs`, `list_containers`, `container_logs` and `list_ports`, plus instructions to reuse running servers rather than start their own. Both log tools put each line's local time before it and take `match`, a case-insensitive regular expression, `context`, lines around each match, and `since`, a duration such as `10m` or an RFC 3339 time. A container search covers its last 20,000 lines; a server's line times come from when the daemon received the output, to the second. Replies stay under 256 KiB with long lines cut, and bracketed notes at the top say what was left out. Without MCP, `portside ensure <name> --json`, `portside status --json`, `portside containers --json` and `portside logs <name> --grep <regexp> -C <n>` give the same information.

Install the agent skill too:

```sh
portside skill install
```

It writes `SKILL.md` to `~/.claude/skills/portside/` and `~/.codex/skills/portside/` (once when one links to the other) for the agents set up on the account. The skill covers when to reuse, start or restart a server, how to diagnose one through its logs and containers, and what to leave alone. The daemon rewrites an installed copy when it starts, so the skill follows app updates; a file with that name that `skill install` did not write is kept unless you pass `--force`. `portside skill print` shows the file, and `portside doctor` reports, for each agent on the account, whether it has the MCP server and the current skill, with the command that fixes it.

## Other computers

Work on one computer while the projects run on another, for example a laptop driving a desktop.

1. Install Tailscale on both and sign both in to the same Tailscale account.
2. On the computer that runs the projects, turn sharing on: **Computers** in the top bar, then **Let my other devices connect**, or `portside share on` (it sets `share = true` in the config). Either shows a one-time pairing code, valid for 10 minutes.
3. On the computer you work on, open **Computers**, choose **Pair** next to the other computer and enter the code. Pairing is needed once per device; afterwards **Connect** is enough.

`portside share code` prints a new code, `portside share devices` lists paired devices and `portside share revoke <device>` unpairs one, which also ends its connection. The **Computers** window on the shared computer does the same.

The window changes color and shows the other computer's name in the top bar. Everything in the dashboard then acts on that computer: servers, logs and terminals, containers, ports, adding servers and editing its config. Opening a folder, an editor or a terminal is not offered, because it would happen on the other computer's screen. **Disconnect** returns to this computer; the connection belongs to the daemon, so every dashboard window follows it, and it ends when the daemon stops.

Sites open as if they ran here:

- While connected, the port of each running remote server (and each published container port, such as a database) is forwarded to the same port on this computer, so `http://127.0.0.1:5173` and `psql -p 5432` reach the other computer. A port already in use here, on any address, is left alone and listed as busy. Starting a local server on a forwarded port takes the port back. Like any local server, a forwarded port can be reached by every account on this computer.
- Every remote server is also at `http://<name>.<computer>.localhost:7878`, for example `http://web.devbox.localhost:7878`. A link to `http://<name>.localhost:7878` copied from the other computer works too when this computer has no server of that name.

Only devices signed in to the same Tailscale user that are not tagged appear in the list. Sharing uses Tailscale Serve with HTTPS: the daemon publishes `https://<computer>.<tailnet>.ts.net:7878` inside your tailnet, which `tailscale serve status` lists, and withdraws it when sharing is turned off or the daemon stops. It refuses to replace a Serve entry on that port that it did not create. The tailnet must have HTTPS certificates enabled (Tailscale admin console, DNS page). On Linux, allow Serve for your account once with `sudo tailscale set --operator=$USER`. Discovery checks each online device on ports 7878 and this daemon's own port.

## How it works

- `portside daemon` owns every server process. It listens on `127.0.0.1:7878` (and `[::1]`) and serves the API, the dashboard (embedded in the binary) and the `*.localhost` proxy, routed by the `Host` header.
- Each server runs as `bash -c <cmd>` in its own pseudo-terminal and process group. Output goes to a 1 MiB in-memory scrollback and to `~/.local/state/portside/logs/<name>.log` (rotated at 10 MiB).
- State (which servers you want running, and the process groups in flight) is saved to `~/.local/state/portside/state.json`. After a crash of the daemon itself, leftover process groups are verified by start time and stopped before servers are restored.
- The macOS app (`macos/`, Swift) bundles the same binary in `Portside.app/Contents/Helpers/portside`, starts the daemon if needed and follows its event stream. Quitting the app leaves the daemon and your servers running; **Stop Daemon and Servers** in its menu stops them. **Launch at Login** registers the app as a login item.
- Containers come from the Docker Engine API on the socket of the active Docker context (`DOCKER_HOST`, `DOCKER_CONTEXT`, then `currentContext` in `~/.docker/config.json`), read from your login-shell environment. An event stream keeps the list current and a stats stream per running container feeds CPU and memory. A container belongs to the server whose `dir` is its `com.docker.compose.project.working_dir`; when several servers share that directory, the one running `docker compose` wins, else the first. Recreate and rebuild run `docker compose up --detach --no-deps --force-recreate [--build] <service>` with the project's own files, because the engine API has no notion of Compose projects.
- `portside service install` writes a launchd agent (`~/Library/LaunchAgents/app.foreverbeta.portside.plist`) or a systemd user unit.

### Security model

The daemon runs arbitrary commands from your config, so its API must only be reachable by you:

- it binds to loopback only;
- every API call needs the token in `~/.local/state/portside/auth-token` (mode `0600`, created on first start). The CLI, the MCP server and the macOS app send it; a browser gets an `HttpOnly`, `SameSite=Strict` session cookie through a one-time link that `portside open` or the app creates. Other accounts on the same machine cannot use the API;
- requests whose `Host` is not `127.0.0.1`, `localhost`, `[::1]` or `portside.localhost` are rejected, which defeats DNS rebinding;
- state-changing requests and WebSockets from browsers must come from the dashboard's own origin;
- killing a foreign process is limited to processes currently listening on a port, and refuses Docker, OrbStack, launchd, systemd and the AirPlay receiver.

- sharing is off by default. When it is on, Tailscale Serve forwards tailnet requests to a loopback listener that answers only under a random path known to the Serve configuration. A request is accepted only when all of these hold: it comes from another device of the user who owns this computer's node, not tagged (Tailscale sets `Tailscale-User-Login` and the source address itself); it does not come from this computer, so other accounts, apps and containers here cannot use the tailnet way in; it carries a header that web pages cannot send without a CORS preflight, which is never granted; and it is signed with the key this daemon issued to that same device when it was paired with a one-time code. Tailscale identifies devices, not accounts or processes, so the key is what keeps other accounts on your other devices out. It is never sent after pairing: each request carries an HMAC of its method, URI, time, a single-use nonce and body. Every connection goes over HTTPS with the certificate Tailscale holds for the shared computer's tailnet name, and every reply, WebSocket upgrades included, must also prove with the same key that it comes from the paired daemon, so a program posing as the shared daemon gets neither the code nor anything reusable, and its answers are refused. Keys are stored in `share-devices.json` on the shared computer and `remote-tokens.json` on the connecting one, both mode `0600` in the state directory. A paired device has the same control you have here, which includes running any command through a server definition; the dashboard does not offer it stopping the daemon, opening desktop apps or changing sharing, but that is a convenience, not a security boundary. Pair only devices you trust as much as this one;
- project icons are served only as images, with a sandboxing Content-Security-Policy, so an SVG in a project cannot run script on the dashboard's origin.

Programs running as your own user can read the token, just as they could run the commands themselves. The `*.localhost` proxy needs no token: it only forwards to dev servers that already listen on loopback.

## Migrating from devctl

```sh
portside import devctl ~/dev/devctl/servers.conf --dry-run   # preview
portside import devctl ~/dev/devctl/servers.conf
```

Ports are computed exactly as devctl did, comments above entries are kept, `NODE_BIN` becomes `path`, and servers sharing a name prefix (`shop-web`, `shop-api`) are grouped. devctl's files are not modified. While both tools run, Portside shows servers started by devctl as `external`; stop them in devctl, or use **Stop it and start** to hand them over.

## Development

```sh
make dev-daemon     # isolated daemon on :7980 (config and state in .dev/)
make dev-open       # sign the browser in (the session cookie is shared by 127.0.0.1 ports)
make dev-ui         # dashboard with hot reload on http://127.0.0.1:5173
make build          # dashboard + bin/portside
make snapshot       # release archives in ./dist (goreleaser)
make app            # macOS app in dist/macos/Portside.app (scripts/build-macos-app.sh)
```

A `v*` tag runs `.github/workflows/release.yml`: GoReleaser publishes the CLI archives for macOS (arm64) and Linux (amd64, arm64), then the app is built and attached to the release as `Portside-<version>.zip`. When a `HOMEBREW_TAP_TOKEN` secret exists, the cask `Casks/portside.rb` is also published to `padmk/homebrew-tap`; without it that step is skipped. CI builds on Linux for every push; the macOS build runs from the Actions tab (`ci` workflow, "Run workflow") and on release tags. The app is signed ad hoc unless `SIGN_IDENTITY` is set; notarization needs an Apple Developer account.

## License

MIT
