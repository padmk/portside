# Configuration

## Where things live

The config is `~/.config/portside/portside.toml` (`$PORTSIDE_CONFIG` overrides it). State, logs and the token are in `~/.local/state/portside` (`$PORTSIDE_STATE_DIR` overrides it). `portside service install` pins the config and state locations it was run with in the service definition.

On Linux, `$XDG_CONFIG_HOME` and `$XDG_STATE_HOME` move both defaults. On macOS they do not, because the app and launchd never see a shell's variables; this way the app, the CLI and the service always share one config, state directory, token and lock.

On macOS, when those variables point elsewhere and the default location does not exist yet, the next command moves the config and state directories found there to the defaults and says so:

- Only a directory holding nothing but Portside's own files is moved (`portside.toml` in the config directory, `daemon.lock` or `state.json` in the state directory). One with other files, such as a repository named `portside`, stays in place with a note.
- While a daemon still runs from the old state directory, commands keep using the old locations until it stops. While the login service still names them, they stay until `portside service install` moves them and rewrites the service.

## Editing and reloading

The first run writes a commented starter file. Its `root` is your projects folder: of the folders people commonly keep code in, directly in your home folder or in `~/Documents` (such as `~/Developer`, `~/dev`, `~/code`, `~/projects`, `~/src`, `~/repos`, `~/workspace`, `~/Documents/GitHub` or the JetBrains `~/IdeaProjects`, in any letter case), the one with the most projects one or two levels down, else your home folder.

The daemon reloads the file when it changes, at the latest when the next command or request reaches it, so `portside up` right after `portside add` or an edit in your editor sees the new server. Edits from the dashboard keep your comments and formatting. A server added or edited with a missing working directory is saved with a warning; create the directory before starting it. Unknown keys are ignored with a warning. Two keys in one table that differ only in case, such as `cmd` and `Cmd`, would set the same setting, so the file is refused; keys under `env` keep their case.

When the file is invalid:

- The last good settings stay live, also across daemon restarts. Without a saved copy, the dashboard starts with an empty list of servers so you can repair the file. The banner shows the error.
- `portside daemon restart` refuses an invalid config before replacing a healthy daemon.

A dashboard edit made from an older version of the file (because it changed on disk while the form or the config editor was open) is refused instead of overwriting that change; the dashboard then offers to reload, or in the config editor to overwrite. After a save, the dashboard offers to restart the running servers that still use the old settings.

## Example

```toml
root = "~/dev"                      # base for relative dirs
# port = 7878                       # dashboard, API and *.localhost proxy
# path = ["/opt/homebrew/opt/node@24/bin"]   # prepended to PATH for every server (Homebrew;
#                                   # with nvm: "~/.nvm/versions/node/v24.11.0/bin")
# env = { NODE_OPTIONS = "--max-old-space-size=4096" }   # for every server
# restore = true                    # bring back running servers after a restart
# notify = true                     # desktop notifications: crashes, container problems,
#                                   # devices pairing or connecting (sharing)
# stop_timeout = "10s"              # grace period after Ctrl-C
# shell = "bash"                    # runs each cmd as <shell> -c <cmd>
# editor = "Zed"                    # app (macOS) or command for "Open in editor"
# terminal = "Terminal"             # app (macOS) or command for "Open in Terminal"
# docker_start = "colima start"     # how "Start Docker" starts the engine; detected by default
# share = false                     # let your other devices on Tailscale connect (see other-computers.md)
# on_crash = "~/bin/report"         # run when a server crashes for good (see Hook commands)
# on_unready = "~/bin/report"       # run when a running server stops answering
# on_recover = "~/bin/report"       # run when a server reported by either is ready again
# login_env = true                  # false: servers get the daemon's own environment
# umask = "0007"                    # mode mask for files servers create; the daemon's by default

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
cmd = "docker compose up"
port = 5432
group = "shop"
stop_timeout = "30s"
# projects = ["shop-db", "shared-cache"]  # explicit Compose project attachment
```

## Top-level keys

| Key | Meaning |
| --- | --- |
| `root` | Base for relative server `dir`s. A relative `root` is relative to the config file's folder, which is also the default. |
| `port` | The daemon's port for the dashboard, API and `*.localhost` proxy. Without it, 7878 or a fallback ([The daemon's port](#the-daemons-port)). No server may use it. |
| `path` | Folders put in front of `PATH` for every server, the hook commands and the tools Portside runs for you ([Environment](#environment)). |
| `env` | Environment variables for every server; a server's own `env` wins. `{PORT}` works in values. |
| `restore` | `true` (default) starts the servers that were running when the daemon stopped, such as after a reboot. |
| `notify` | `true` (default) shows desktop notifications for crashes, container problems, and devices pairing or connecting ([Other computers](other-computers.md)). |
| `stop_timeout` | Default grace period after `Ctrl-C`, `"10s"` unless set. |
| `shell` | Shell that runs each `cmd` as `<shell> -c <cmd>`, default `bash`, looked up on the servers' `PATH` (`/bin/sh` when it is not found). |
| `editor`, `terminal` | What **Open in editor** and **Open in Terminal** use ([Editor, terminal, shell and Docker](#editor-terminal-shell-and-docker)). |
| `docker_start` | Command **Start Docker** runs; without it, the engine behind the active Docker context is started. |
| `share` | `true` lets your other devices on Tailscale connect ([Other computers](other-computers.md)). Default `false`. |
| `on_crash` | Command run when a server crashes for good ([Hook commands](#hook-commands)). |
| `on_unready`, `on_recover` | Commands run when a running server stops answering, and when a server one of the hooks reported is ready again ([Hook commands](#hook-commands)). |
| `unready_after` | How long a running server must go without answering before `on_unready` runs, `"1m"` unless set. |
| `retry_after` | For servers with `restart = "on-failure"` or `"always"`: how long after the automatic restarts gave up (or a start failed) to start the server again, such as `"5m"`; unset, it stays down. |
| `login_env` | `true` (default) gives servers the environment of your login shell, `false` the daemon's own ([Environment](#environment)). |
| `umask` | File mode creation mask for every server, such as `"0007"`; without it servers get the daemon's ([File permissions](#file-permissions)). |

Paths in `root`, `dir`, `path` and `icon` may start with `~`.

## Server keys

| Key | Meaning |
| --- | --- |
| `cmd` | Shell command (required), run with the `shell` setting (`bash -c` by default). `{PORT}` is replaced with the port, and `$PORT` is set unless the port is `0`. |
| `dir` | Working directory, relative to `root` or absolute; `root` itself when omitted. |
| `port` | Port the server listens on. Omit it to assign the lowest free port from 3001 in file order (skipping the daemon's port, ports of temporary worktree servers, AirPlay's 5000 and 7000 and browser-blocked ports), written into the file when the daemon loads it. `0` means no port: such a server counts as running 1.5 s after it starts (see `ready_log` and `ready_after`). Two servers with the same port, or a reserved one, load with a warning. |
| `group` | Name for starting and stopping related servers together. |
| `depends_on` | Servers that must be running (and ready) before this one starts. Unknown names and cycles are errors. |
| `restart` | `no` (default), `on-failure` (after a non-zero exit) or `always` (after any exit). Restarts wait 1 s, then 2, 4, 8 and 16 s; after 5 restarts in a row the server stays `crashed`, or starts again after `retry_after`. A run longer than a minute starts the count again. |
| `health` | HTTP path that must answer with a success or a redirect (status 200 to 399, redirects not followed) before the server counts as running. A running server that stops answering is shown as [not responding](dashboard.md#server-states); it is not restarted. |
| `ready_log` | Regular expression that a whole line of the server's output (ended by a newline) must match before it counts as running, in addition to its port and `health`; for example `ready_log = "Worker ready"` for a worker without a port. Until then it stays `starting`, and so do servers that `depends_on` it. An invalid expression is an error. |
| `ready_after` | How long the process must run before it counts as running, such as `"10s"`; without it, a server with a port counts as soon as the port answers, and one without a port after 1.5 s. |
| `env` | Extra environment variables. `{PORT}` works in values. The dashboard hides secret-looking values until revealed. |
| `stop_timeout` | How long to wait after `Ctrl-C` before `SIGTERM` (`SIGKILL` follows 3 s later); the top-level `stop_timeout` by default. |
| `on_crash`, `on_unready`, `on_recover` | Commands for this server instead of the top-level ones; `""` runs none for it ([Hook commands](#hook-commands)). |
| `unready_after` | This server's wait before `on_unready`, instead of the top-level one. |
| `retry_after` | This server's wait before it is started again after giving up, instead of the top-level one; `"0s"` turns retries off for it. Each retry gets another 5 restarts, until the server runs or you stop it; a pending retry keeps its time across a daemon upgrade in place. Without `on-failure` or `always` it has no effect (a warning). |
| `umask` | This server's file mode creation mask, instead of the top-level one ([File permissions](#file-permissions)). |
| `enabled` | `false` leaves the server out of `--all` and restore; it can still be started by name or group. |
| `icon` | Icon for the dashboard, relative to `dir` or absolute; by default a project favicon is detected. |
| `open` | Path appended to the URL when opening the server, such as `/admin`. |
| `projects` | Compose project names (or Supabase CLI project IDs) attached to this server, overriding directory matching for those projects ([Containers](containers.md)). Names must be unique within the list, start with a lowercase letter or digit, and contain only lowercase letters, digits, dashes or underscores. |
| `lan` | `true` when the server is meant to be reachable from other devices: listening on the network is then not pointed out, for it or for the containers it owns ([Security](security.md#servers-reachable-from-the-network)). |
| `wrap` | How the command enters the project's tool environment ([Version managers](#version-managers)): `auto` (default) detects it, `none` runs the command as is, anything else is a command prefix such as `nix develop -c` (words separated by spaces). |

Server names are lowercase letters, digits and dashes (at most 63 characters, not starting or ending with a dash), because they double as `<name>.localhost` hostnames. `portside` and names of the form `d-` followed by 24 hexadecimal digits are reserved for the dashboard; an existing entry named `portside` loads with a warning. Renaming a running server in the dashboard keeps its process and output.

## Hook commands

Three commands report what happens to servers, also when desktop notifications are off or the app is open, so a server without a desktop can report to your monitoring:

- `on_crash` runs when a server has crashed and will not be restarted automatically:
  - with `restart = "no"`, at the first crash;
  - with `on-failure` or `always`, after the last automatic restart, and with `always` also for a server that keeps exiting with code 0;
  - when the server cannot start, for example because its directory is missing, another program holds its port or a dependency failed.
- `on_unready` runs when a running server has not answered on its port or `health` path for `unready_after` (a minute unless set), once until it answers again. The dashboard shows such a server as [not responding](dashboard.md#server-states) after 4 seconds; the longer wait keeps a hot reload or a short stall from being reported.
- `on_recover` runs when a server that `on_crash` or `on_unready` reported is ready again: restarted by you, an agent or your monitoring, or answering again.

Each runs once per event. Set at the top level, a command applies to every server; a server's own replaces it, and `""` turns it off for that server. One script can handle all three by `PORTSIDE_EVENT`:

```toml
on_crash = "~/bin/report"           # every server
on_unready = "~/bin/report"
on_recover = "~/bin/report"

[servers.worker]
cmd = "pnpm worker"
port = 0
on_crash = ""                       # except this one
```

```sh
#!/bin/sh
# ~/bin/report: post the event to a Slack or Mattermost webhook ({"text": ...};
# Discord takes {"content": ...}). jq builds the JSON, so quotes in the note are safe;
# --retry rides out a short outage of the webhook. The URL holds a secret, so it
# stays in a curl config file only you can read (chmod 600) with one line,
# url = "https://hooks.example.com/...", out of the config and of the process list.

# A server that keeps failing, also when a timer starts it again, is reported once.
[ "$PORTSIDE_EVENT" = crashed ] && [ "$PORTSIDE_STREAK" -gt 1 ] && exit 0
jq -n --arg text "$PORTSIDE_SERVER on $(hostname): $PORTSIDE_EVENT, $PORTSIDE_NOTE" '{text: $text}' |
  curl -fsS --retry 3 --retry-all-errors -H 'Content-Type: application/json' --data-binary @- \
    -K ~/.config/report/curlrc
```

The command runs with `sh -c` in your home directory. Its environment is the servers' base environment (your login shell's, or the daemon's own with `login_env = false`), `path` and the top-level `env` (without the server's own `env`), plus these variables; the details never appear in its command line:

| Variable | Value |
| --- | --- |
| `PORTSIDE_EVENT` | `crashed`, `unready` or `recovered`. |
| `PORTSIDE_SERVER`, `PORTSIDE_GROUP` | The server's name and group (empty without one). |
| `PORTSIDE_TRIGGER` | What began the server's current round of runs, which its automatic restarts continue: `start` (by you, a tool, an agent, or a server that depends on it), `retry` (after `retry_after`) or `restore` (the daemon brought it back when it started). |
| `PORTSIDE_STREAK` | For `crashed`: how many times in a row the server has crashed for good without being ready in between, `1` for the first. Empty for other events. |
| `PORTSIDE_EXIT_CODE` | For `crashed`: the exit code, empty when the server could not start. Empty for other events. |
| `PORTSIDE_UPTIME` | Seconds the last run lasted, or the current run has lasted so far. |
| `PORTSIDE_RESTARTS` | Automatic restarts in the current round; for `crashed`, before it gave up (`0` with `restart = "no"`). |
| `PORTSIDE_RECOVERED_FROM` | For `recovered`: the last problem reported, `crashed` or `unready`. |
| `PORTSIDE_DOWNTIME` | For `recovered`: seconds since that problem began (the first crash reported, or when the server stopped answering). |
| `PORTSIDE_NOTE` | The note `portside status` shows, such as `exited with code 2; gave up after 5 restarts` or `health /healthz answers 503`. |
| `PORTSIDE_LOG_TAIL` | The last 20 non-empty lines of the log (at most 8 KiB, without colors), with the start and exit lines Portside writes there. Output can hold secrets, so treat it as you treat the logs. |
| `PORTSIDE_LOG_FILE` | The server's full log file. |

The streak and a problem not yet recovered from are kept in the state directory, so restarting or upgrading the daemon neither repeats nor forgets them.

The daemon does not wait for the commands and runs at most four at once. One still running after 30 seconds is stopped, and a background process it leaves holding its output does not keep it running; either result is in the daemon log (`portside daemon logs`). Delivery is best effort: a failed command is not retried, and a report is lost when the daemon stops while it runs, so let your monitoring also check the daemon itself ([Linux server](linux-server.md#reading-state-from-scripts)). `portside doctor` shows how many servers have `on_crash` and `on_unready`, and on Linux warns when `notify` is on but no notification can be shown (no `notify-send`, or a daemon without a display), because a crash would then notify no one.

## Editor, terminal, shell and Docker

`editor` and `terminal` choose what **Open in editor** and **Open in Terminal** use; the dashboard's **Settings** list the ones found on this computer and save the choice here. Both accept an app name on macOS or a command with arguments. A `terminal` command receives the directory as its last argument; use `{DIR}` to place it elsewhere, for example `terminal = "foot --working-directory={DIR}"`. Known apps and commands (such as `Ghostty`, `kitty`, `ptyxis` or `konsole`) get the right arguments by name.

Without `editor`, Portside uses `$VISUAL` or `$EDITOR` from your login shell when it names a GUI editor, else the first installed of Cursor, Visual Studio Code, Zed, Windsurf, Sublime Text, VS Code Insiders, VSCodium, Nova, BBEdit, the JetBrains IDEs (IntelliJ IDEA, WebStorm, PyCharm, GoLand, RustRover, PhpStorm) and Xcode, else the system's default app. On macOS it finds the apps by bundle ID, so their command-line launchers need not be installed; on Linux it looks for their commands (`cursor`, `code`, `zed`, `windsurf`, `subl`, `code-insiders`, `codium`, `idea`, `webstorm`, `pycharm`, `goland`, `rustrover`, `phpstorm`).

Without `terminal`, macOS opens the first installed of Ghostty, iTerm2, WezTerm, kitty, Alacritty and Warp, else Terminal. Linux uses `$TERMINAL`, then `x-terminal-emulator`, then the first of `ghostty`, `wezterm`, `kitty`, `alacritty`, `foot`, `ptyxis`, `gnome-terminal`, `kgx`, `konsole` and `xfce4-terminal`.

`shell` is the shell that runs each server's `cmd`. devbox and nvm environments ([Version managers](#version-managers)) need a POSIX shell such as `bash` or `zsh`, not `fish`. `docker_start` is the command **Start Docker** runs; by default the engine behind the active Docker context is started ([Containers](containers.md)).

## Environment

launchd and systemd often start the daemon with a bare environment, so it reads `PATH` and the rest of your environment from your account's login shell (the one set for your user, not `$SHELL`):

- zsh, bash and fish are read as interactive login shells, so their rc files count; sh, ksh, mksh and dash as login shells.
- For other shells, such as nushell or tcsh, it reads a login zsh (else bash) instead, so add entries that only your shell's own config sets with `path`.
- The read is repeated every 10 minutes. When it fails or takes over 10 seconds, servers get the daemon's own environment.
- An `ssh-agent` that your rc files start during a read is stopped again, and servers keep the daemon's own agent.

`portside doctor` shows which shell was read, how, and the resulting `PATH`.

`login_env = false` skips the login shell: servers get the daemon's own environment, which on Linux is what the systemd user service sets, the same however your rc files change. Put what servers need there in `path` and `env`; tools that only your rc files put on `PATH` (such as nvm or fnm) are then not found.

A server's environment is that login-shell environment, with `path` in front of `PATH`, then the top-level `env` and the server's `env`. Portside adds `PORT` (unless the port is `0`), `FORCE_COLOR=1`, `TERM=xterm-256color` and `COLORTERM=truecolor`; commands detected as Compose stacks also get `COMPOSE_MENU=false`, which keeps interactive Compose menus out of their logs.

Running servers are marked stale until restarted when the login-shell environment (or `login_env`), the top-level `env`, `path`, `shell`, a server's `env`, `wrap` or `umask`, or the tool a server would start under (such as a new `.envrc`) changes. Values that differ on every shell start do not count:

- variables that changed between the first two reads;
- per-session variables of terminals and tools, such as `FNM_*`, `ATUIN_*`, `__MISE_*`, `KITTY_*`, `TERM_SESSION_ID` or a temporary `SSH_AUTH_SOCK`;
- fnm's per-shell `PATH` entries.

### File permissions

Files a server creates get the daemon's umask, which launchd and systemd set to `0022` unless told otherwise, so other accounts on the machine can read them. On a machine shared with other accounts, set `umask = "0007"` (your group may read and write) or `"0077"` (only you) at the top level or for a server. Portside runs `umask` in the server's shell before its command, so the `shell` must support it, as sh, bash, zsh and fish do. Files Portside writes itself are readable only by your account whatever this says ([Security](security.md#trust)).

## Version managers

The environment is read once in your home folder, so tool versions that your terminal switches on `cd` would not apply. When a server starts, Portside looks in its directory and its parents up to the repository root (only the directory itself outside a repository). It runs the command through the first tool in this list that has a file there and is on the server's `PATH`:

1. `.envrc`: `direnv exec`. An `.envrc` you have not allowed is skipped with a note in the log.
2. `devbox.json`: `devbox shellenv --init-hook`, loaded into the server's shell, so the project's `init_hook` runs too.
3. `mise.toml`, `.mise.toml`, `.config/mise.toml`, `mise/config.toml` or `.tool-versions`: `mise exec`, which never installs tools during a start; mise reports missing ones in the log.
4. `.nvmrc` or `.node-version`: `fnm exec --using`, or, for `.nvmrc` without fnm, nvm loaded from `$NVM_DIR/nvm.sh` (default `~/.nvm`) and `nvm use`.

Node version files apply only to commands that run Node tools (such as `node`, `npm`, `pnpm`, `yarn`, `vite` or `next`) or to a server directory with a `package.json`. When fnm or nvm has not installed that version, the command runs with the default `node` and a note in the log. Commands that run Docker Compose, `docker`, `podman` or `nerdctl` are never wrapped.

`$PORT` and the variables from the top-level and server `env` keep Portside's values even when the project's environment sets them (for example `PORT` in a direnv `dotenv` file). `PATH` is the exception: a `PATH` from `env` or `path` is where the tool starts, and the tool's own entries come before it.

Nix flakes work through direnv (`use flake`) or `wrap = "nix develop -c"`. A `wrap` command that is not on the server's `PATH` is skipped with a note in the log. The tool in use appears in the server's log, in its details in the dashboard and as `tool` in `portside status --json`; `wrap = "none"` turns detection off for a server.

## The daemon's port

The daemon listens on port 7878 unless `port` is set. Anything that accepts connections on a port at `127.0.0.1` or `::1` counts as taken, including a program listening on all addresses.

When the file sets no `port` and 7878 is taken by another program (Radarr uses it too) or by the daemon of another account, the daemon takes the first free port from 7879 to 7899 that no server uses, then from 17879 to 17899, and logs the change. It keeps that port in `auto-port` in the state directory, not in the config, so the next start uses it again while it is free and the port stays automatic. The CLI, the app, agents and your other computers follow it (`daemon.port` in the state directory; connected computers try the same ranges). When every port of both ranges is taken, the daemon stops with an error asking for `port`.

It never moves away from a port that another Portside daemon of your account holds, such as one started with another `PORTSIDE_STATE_DIR` for the same config, or an isolated instance without `port`. The daemon refuses to start, and the daemon and the CLI name the holder's pid and state directory and leave the config alone; set `port` for isolated instances. A holder counts as such a daemon only when `lsof` or `ss` shows a `portside` process of your account listening. Anything else, including an `ssh -L` tunnel to another computer's daemon or a process that only claims to be Portside, counts as another program.

A `port` set in the file that is taken stops the daemon with an error naming the holder where `lsof` or `ss` can see it. An in-place upgrade (`portside daemon restart`) that finds a changed `port` in the config moves there when it is free, and otherwise keeps the old port until the next restart.

## Project discovery

`portside scan [dir]` (and **Find projects** in the dashboard) walks the config's `root`, or the given folder, and proposes servers with the right port flags. It finds:

- Node dev servers: Next.js, Vite, Astro, Nuxt, Remix, React Router, Angular, Gatsby, webpack-dev-server, Vue CLI, Docusaurus, Create React App and Wrangler;
- Deno tasks;
- Django, Flask and ASGI apps (FastAPI, Starlette);
- Rails, Laravel, Phoenix, Spring Boot, ASP.NET Core, Go and Rust web servers;
- Compose projects.

Node port flags are added only when the script runs one of those CLIs itself, also through other scripts (`npm run`, `pnpm`, `yarn`, `bun run`, `node --run`) that pass the flags on, package binaries (`npx`, `pnpm vite`, `yarn vite`) and environment assignments. Other scripts get a port when they use `$PORT` or their entry file reads `process.env.PORT` or starts Next.js (keeping a port the entry file passes to `next` itself, such as `"-p", "3000"`); otherwise none.

Workspace roots (a `workspaces` list, `pnpm-workspace.yaml` packages, or a root turbo, nx or lerna config with packages; turbo, nx, `pnpm -r` and similar scripts) are left out when their apps are found, including packages outside the usual folders that the workspace lists; otherwise they are proposed without a port.

Python commands use, in this order:

1. the project's tool by lockfile in the project or a parent up to the repository root (`uv run`, `poetry run`, `pipenv run`, `pdm run`);
2. `uv run` when uv is on the servers' `PATH` (the login shell's, for the dashboard and `scan` alike) and `pyproject.toml` has a `[project]` table, unless a `.venv` that uv did not make (no `uv =` line in its `pyvenv.cfg`) exists and `pyproject.toml` has no `[tool.uv]` section;
3. a `.venv` or `venv` interpreter;
4. `python3`.

A configured command run another of these ways counts as the same command. Declared `[project.scripts]` entry points need one of those environments and have no assumed listening port. Go and Rust servers get a port only when their source reads `PORT`.

Discovery visits each folder once, however many symlinks lead to it, skips commands already configured, keeps other services in the same folder, and checks live listeners when suggesting automatic ports.

`portside scan` only lists; `portside scan --add` adds every proposal at once (on a terminal it asks first, unless `--yes`), and **Find projects** adds the ones you tick. A server added this way runs the project's own scripts (its `dev` script, `docker compose up`) when anyone starts it, including a coding agent through `ensure_server`, so leave out repositories you would not run yourself.
