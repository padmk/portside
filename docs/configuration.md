# Configuration

## Where things live

The config lives at `~/.config/portside/portside.toml` (`$PORTSIDE_CONFIG` overrides it) and state, logs and the token at `~/.local/state/portside` (`$PORTSIDE_STATE_DIR` overrides it). `portside service install` pins the config and state locations it was run with in the service definition.

On Linux `$XDG_CONFIG_HOME` and `$XDG_STATE_HOME` move both defaults; on macOS they do not, because the app and launchd never see a shell's variables, so the app, the CLI and the service always agree on one config, state directory, token and lock. Earlier versions followed them on macOS too: when they point elsewhere and the default location does not exist yet, the next command moves the config directory and state directory from there to the defaults and says so. Only a directory holding Portside's own files is moved (`portside.toml` in the config directory, `daemon.lock` or `state.json` in the state directory, and nothing else); one with other files, such as a repository named `portside`, is left in place with a note. While a daemon still runs from the old state directory, commands keep using the old locations until it stops, and while the login service still names them, until `portside service install` moves them and rewrites the service.

## Editing and reloading

The first run writes a commented starter file whose `root` is your projects folder: of the folders people commonly keep code in (such as `~/Developer`, `~/dev`, `~/code`, `~/projects`, `~/src`, `~/repos`, `~/workspace`, `~/Documents/GitHub` or the JetBrains `~/IdeaProjects`, in any letter case), the one with the most projects one or two levels down, else your home folder.

The daemon reloads the file when it changes, at the latest when the next command or request reaches it, so `portside up` right after `portside add` or an edit in your editor sees the new server; edits from the dashboard keep your comments and formatting. Adding or editing a server with a missing working directory saves it and displays a warning; create the directory before starting it.

An invalid file keeps the last good settings live, including across daemon restarts; without a saved copy the dashboard starts with an empty roster so you can repair the file. The banner shows the error, and `portside daemon restart` refuses an invalid config before replacing a healthy daemon. A dashboard edit made from an older version of the file (because it changed on disk while the form or the config editor was open) is refused instead of overwriting that change; the dashboard then offers to reload, or in the config editor to overwrite. After a save, the dashboard offers to restart the running servers that still use the old settings.

## Example

```toml
root = "~/dev"                      # base for relative dirs
# port = 7878                       # dashboard, API and *.localhost proxy
# path = ["/opt/homebrew/opt/node@24/bin"]   # prepended to PATH for every server (Homebrew;
#                                   # with nvm: "~/.nvm/versions/node/v24.11.0/bin")
# restore = true                    # bring back running servers after a restart
# notify = true                     # desktop notifications: crashes, container problems,
#                                   # devices pairing or connecting (sharing)
# stop_timeout = "10s"              # grace period after Ctrl-C
# shell = "bash"                    # runs each cmd as <shell> -c <cmd>
# editor = "Zed"                    # app (macOS) or command for "Open in editor"
# terminal = "Terminal"             # app (macOS) or command for "Open in Terminal"
# docker_start = "colima start"     # how "Start Docker" starts the engine; detected by default
# share = false                     # let your other devices on Tailscale connect (see other-computers.md)
# on_crash = "~/bin/report-crash"   # run when a server crashes for good (see Crash command)

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

## Server keys

| Key | Meaning |
| --- | --- |
| `dir` | Working directory, relative to `root` or absolute. |
| `cmd` | Shell command, run with `bash -c`. `{PORT}` is replaced; `$PORT` is set too. |
| `port` | Port the server listens on. Omit it to assign a free port from 3001 (skipping ports of temporary servers), saved to the file on daemon load; `0` for none, and such a server counts as running 1.5 s after it starts (see `ready_log` and `ready_after`). |
| `group` | Name for starting and stopping related servers together. |
| `projects` | Explicit Compose project names attached to this server, overriding directory matching for those projects. Names must be unique within the list, start with a lowercase letter or digit, and contain only lowercase letters, digits, dashes or underscores. |
| `depends_on` | Servers that must be running (and ready) before this one starts. |
| `restart` | `no` (default), `on-failure` (after a non-zero exit) or `always` (after any exit). Restarts wait 1 s, then 2, 4, 8 and 16 s; after 5 restarts in a row the server stays `crashed`. A run longer than a minute starts the count again. |
| `health` | HTTP path that must answer with a success or a redirect (status 200 to 399) before the server counts as running. A running server that stops answering gets a note in its status; it is not restarted. |
| `ready_log` | Regular expression that a line of the server's output must match before it counts as running, in addition to its port and `health`; for example `ready_log = "Worker ready"` for a worker without a port. Until then it stays `starting`, and so do servers that `depends_on` it. |
| `ready_after` | How long the process must run before it counts as running, such as `"10s"`; without it a server with a port counts as soon as the port answers, and one without a port after 1.5 s. |
| `on_crash` | Command run when this server crashes for good, instead of the top-level `on_crash`; `""` runs none for it ([Crash command](#crash-command)). |
| `env` | Extra environment variables. `{PORT}` works in values. The dashboard hides secret-looking values until revealed. |
| `stop_timeout` | How long to wait after `Ctrl-C` before `SIGTERM`. |
| `enabled` | `false` leaves the server out of `--all` and restore; it can still be started by name. |
| `icon` | Path to an icon for the dashboard; by default a project favicon is detected. |
| `open` | Path appended to the URL when opening the server, e.g. `/admin`. |
| `lan` | `true` when the server is meant to be reachable from other devices: listening on the network is then not pointed out, for it or for the containers it owns ([Security](security.md#servers-reachable-from-the-network)). |
| `wrap` | How the command enters the project's tool environment (see [Version managers](#version-managers)): `auto` (the default) detects it, `none` runs the command as is, anything else is a command prefix such as `nix develop -c` (words separated by spaces). |

Server names are lowercase letters, digits and dashes, because they double as `<name>.localhost` hostnames. `portside` is reserved for the dashboard; existing entries with that name load with a warning. Renaming a running server in the dashboard preserves its process and output.

## Crash command

`on_crash` runs a command when a server has crashed and will not be restarted automatically:

- with `restart = "no"`, at the first crash;
- with `on-failure` or `always`, after the last automatic restart, and with `always` also for a server that keeps exiting with code 0;
- when the server cannot start, for example because its directory is missing or a dependency failed.

It runs once per crash, also when desktop notifications are off or the app is open, so a server without a desktop can report crashes to your monitoring. A server's own `on_crash` replaces the top-level one:

```toml
on_crash = "~/bin/report-crash"     # every server

[servers.worker]
cmd = "pnpm worker"
port = 0
on_crash = ""                       # except this one
```

```sh
#!/bin/sh
# ~/bin/report-crash: post the crash to a chat webhook
curl -fsS --data-urlencode "text=$PORTSIDE_SERVER on $(hostname): $PORTSIDE_NOTE" https://hooks.example.com/alert
```

The command runs with `sh -c` in your home directory. Its environment is what servers get from your login shell, `path` and the top-level `env` (without the server's own `env`), plus these variables; the crash details are never put in its command line:

| Variable | Value |
| --- | --- |
| `PORTSIDE_EVENT` | `crashed` |
| `PORTSIDE_SERVER`, `PORTSIDE_GROUP` | The server's name and group (empty without one). |
| `PORTSIDE_EXIT_CODE` | The exit code; empty when the server could not start. |
| `PORTSIDE_UPTIME` | Seconds the last run lasted. |
| `PORTSIDE_RESTARTS` | Automatic restarts before it gave up, `0` with `restart = "no"`. |
| `PORTSIDE_NOTE` | The note `portside status` shows, such as `exited with code 2; gave up after 5 restarts`. |
| `PORTSIDE_LOG_TAIL` | The last 20 lines of the log (at most 8 KiB, without colors), with the start and exit lines Portside writes there. Output can hold secrets, so treat it as you treat the logs. |
| `PORTSIDE_LOG_FILE` | The server's full log file. |

The daemon does not wait for the command, and runs at most four at once. One still running after 30 seconds is stopped, and either result is in the daemon log (`portside daemon logs`). Delivery is best effort: a failed command is not retried, and a report is lost when the daemon stops while it runs, so let your monitoring also check the daemon itself. `portside doctor` shows how many servers have a crash command, and on Linux warns when `notify` is on but no notification can be shown (no `notify-send`, or a daemon without a display), because a crash would then notify no one.

## Editor, terminal, shell and Docker

`editor` and `terminal` choose what **Open in editor** and **Open in Terminal** use; the dashboard's **Settings** list the ones found on this computer and save the choice here. Both accept an app name on macOS or a command with arguments. A `terminal` command receives the directory as its last argument; use `{DIR}` to place it explicitly, for example `terminal = "foot --working-directory={DIR}"`. Known apps and commands (such as `Ghostty`, `kitty`, `ptyxis` or `konsole`) get the right arguments by name.

Without `editor`, Portside uses `$VISUAL` or `$EDITOR` from your login shell when it names a GUI editor, else the first installed of Cursor, Visual Studio Code, Zed, Windsurf, Sublime Text, VS Code Insiders, VSCodium, Nova, BBEdit, the JetBrains IDEs (IntelliJ IDEA, WebStorm, PyCharm, GoLand, RustRover, PhpStorm) and Xcode, else the system's default app. On macOS it finds the apps by bundle ID, so their command-line launchers need not be installed; on Linux it looks for their commands (`cursor`, `code`, `zed`, `windsurf`, `subl`, `code-insiders`, `codium`, `idea`, `webstorm`, `pycharm`, `goland`, `rustrover`, `phpstorm`). Without `terminal`, macOS opens the first installed of Ghostty, iTerm2, WezTerm, kitty, Alacritty and Warp, else Terminal; Linux uses `$TERMINAL`, then `x-terminal-emulator`, then the first of `ghostty`, `wezterm`, `kitty`, `alacritty`, `foot`, `ptyxis`, `gnome-terminal`, `kgx`, `konsole` and `xfce4-terminal`.

`shell` is the shell that runs each server's `cmd` (as `<shell> -c <cmd>`, default `bash`). `docker_start` is the command **Start Docker** runs; by default the engine behind the active Docker context is started.

## Environment

The daemon is often started by launchd or systemd with a bare environment, so it reads `PATH` and the rest of your environment from your account's login shell (the one set for your user, not `$SHELL`) as an interactive login shell, and refreshes it every 10 minutes. sh, bash, zsh, fish, ksh and dash are read directly; for other shells, such as nushell or tcsh, it reads a login `zsh` (else `bash`) instead, so add entries that only your shell's own config sets with `path`. `portside doctor` shows which shell was read, how, and the resulting `PATH`.

Commands detected as Compose stacks receive `COMPOSE_MENU=false` to keep interactive Compose menus out of their logs.

Changes to the login-shell environment, global `env`, `path`, `shell`, a server's `env` or `wrap`, or the tool a server would start under (such as a new `.envrc`) mark running servers stale until restarted. Values in the login-shell environment that differ on every shell start do not: variables that changed between two reads at the first one, per-session variables of terminals and tools (such as `FNM_*`, `ATUIN_*`, `__MISE_*`, `KITTY_*`, `TERM_SESSION_ID` or a temporary `SSH_AUTH_SOCK`) and fnm's per-shell `PATH` entries. An `ssh-agent` that your rc files start during a read is stopped again, and servers keep the daemon's own agent.

Ports 5000 and 7000 (macOS AirPlay) and browser-blocked ports are skipped when assigning ports.

## Version managers

The environment is read once in your home folder, so tool versions that your terminal switches on `cd` would not apply. When a server starts, Portside looks in its directory and the parents up to the repository root and runs the command through the first match whose tool is on the server's `PATH`:

- `.envrc` with `direnv exec` (an `.envrc` you have not allowed is skipped with a note in the log);
- `devbox.json` with `devbox shellenv --init-hook` loaded into the server's shell (so the project's `init_hook` runs too);
- `mise.toml`, `.mise.toml` or `.tool-versions` with `mise exec` (which never installs tools during a start; mise reports missing ones in the log);
- `.nvmrc` or `.node-version` with `fnm exec --using`, or, for `.nvmrc` without fnm, nvm loaded from `$NVM_DIR/nvm.sh` and `nvm use`.

Node version files apply only to commands that run Node tools (such as `node`, `npm`, `pnpm`, `yarn`, `vite` or `next`) or to a server directory with a `package.json`; when fnm or nvm has not installed that version, the command runs with the default `node` and a note in the log. Commands that run Docker Compose, `docker` or `podman` are never wrapped.

`$PORT` and the variables from the global and server `env` keep Portside's values even when the project's environment sets them (for example `PORT` in a direnv `dotenv` file). `PATH` is the exception: a `PATH` from `env` or `path` is where the tool starts, and the tool's own entries come before it. Nix flakes work through direnv (`use flake`) or `wrap = "nix develop -c"`. The tool appears in the server's log, in its details in the dashboard and as `tool` in `portside status --json`; set `wrap = "none"` to turn this off for a server.

## The daemon's port

The daemon listens on port 7878 unless `port` is set. When the file sets no `port` and 7878 is taken by another program (Radarr uses it too) or by the daemon of another account on the same computer, the daemon takes the first free port from 7879 to 7899 that no server uses, then from 17879 to 17899, and logs the change; anything that accepts connections on the port at `127.0.0.1` or `::1` counts as taken, including a program listening on all addresses. It keeps that port in `auto-port` in the state directory, not in the config, so the next start uses it again while it is free and the port stays automatic; the CLI, the app, agents and your other computers follow it (`daemon.port` in the state directory, and connected computers try the same ranges). When every port of both ranges is taken, the daemon stops with an error asking for `port`.

It never moves away from a port that another Portside daemon of your account holds, such as one started with another `PORTSIDE_STATE_DIR` for the same config or an isolated instance without `port`: the daemon refuses to start, and the daemon and the CLI name its pid and state directory and leave the config alone, so set `port` for isolated instances. A holder counts as such a daemon only when `lsof` or `ss` shows a `portside` process of your account listening; anything else, including an `ssh -L` tunnel to another computer's daemon or a process that only claims to be Portside, counts as another program.

A `port` set in the file that is taken stops the daemon with an error naming the holder where `lsof` or `ss` can see it. An in-place upgrade (`portside daemon restart`) that finds a new `port` in the config moves there when it is free and otherwise keeps the old port until the next restart.

## Project discovery

`portside scan` (and **Find projects** in the dashboard) walks the config's `root` and proposes servers with the right port flags. It finds:

- Node dev servers: Next.js, Vite, Astro, Nuxt, Remix, React Router, Angular, Gatsby, webpack-dev-server, Vue CLI, Docusaurus, Create React App and Wrangler;
- Deno tasks;
- Django, Flask and ASGI apps (FastAPI, Starlette);
- Rails, Laravel, Phoenix, Spring Boot, ASP.NET Core, Go and Rust web servers;
- Compose projects.

Node flags are added only when the script runs one of those CLIs itself, also through other scripts (`npm run`, `pnpm`, `yarn`, `bun run`, `node --run`) that pass the flags on, package binaries (`npx`, `pnpm vite`, `yarn vite`) and environment assignments; other scripts get a port when they use `$PORT` or their entry file reads `process.env.PORT` or starts Next.js (which keeps a port the entry file passes to `next` itself, such as `"-p", "3000"`), else none.

Workspace roots (a `workspaces` list, `pnpm-workspace.yaml` packages, or a root turbo, nx or lerna config with packages; turbo, nx, `pnpm -r` and similar scripts) are left out when their apps are found, including packages outside the usual folders that the workspace lists, else proposed without a port.

Python commands use the project's tool by lockfile (`uv run`, `poetry run`, `pipenv run`, `pdm run`), else `uv run` when uv is on the servers' `PATH` (the login shell's, for the dashboard and `scan` alike) and `pyproject.toml` has a `[project]` table, unless a `.venv` that uv did not make (no `uv =` line in its `pyvenv.cfg`) exists and `pyproject.toml` has no `[tool.uv]` section, else a `.venv` or `venv` interpreter, else `python3`; a configured command run another of these ways counts as the same command; declared `[project.scripts]` entry points need one of those environments and have no assumed listening port. Go and Rust servers get a port only when their source reads `PORT`.

Discovery visits each folder once, however many symlinks lead to it, skips commands already configured, keeps other services in the same folder, and checks live listeners when suggesting automatic ports.

`portside scan` only lists; `portside scan --add` adds every proposal at once (on a terminal it asks first, unless `--yes`), and **Find projects** adds the ones you tick. A server added this way runs the project's own scripts (its `dev` script, `docker compose up`) when anyone starts it, a coding agent through `ensure_server` included, so leave out repositories you would not run yourself.
