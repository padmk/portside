# How it works

## The daemon

`portside daemon` owns every server process. It listens on `127.0.0.1:7878` (and `[::1]`) and serves the API, the dashboard (embedded in the binary) and the `*.localhost` proxy, routed by the `Host` header. Its port can move when 7878 is taken; see [Configuration](configuration.md#the-daemons-port).

## Servers, output and state

Each server runs as `bash -c <cmd>` in its own pseudo-terminal and process group. A stop sends `Ctrl-C` to the whole process group first (so `docker compose` shuts its containers down), then `SIGTERM`, then `SIGKILL`; orphaned children are cleaned up.

Output goes to a 1 MiB in-memory scrollback and to `~/.local/state/portside/logs/<name>.log` (rotated at 10 MiB). A daemon restart or upgrade reloads the last 256 KiB from the current and rotated files into scrollback, followed by a `daemon restarted` separator. Restored output has no line timestamps.

State (which servers you want running, the process groups in flight, and last exit codes, times and notes) is saved to `~/.local/state/portside/state.json`. A validated copy of the config is cached as `last-good-config.json` beside it. The config and server logs can contain environment values and credentials, so files in the state directory and the config file are created with mode `0600` and the directories with `0700`; each daemon start removes group and other access from the config file, from `~/.config/portside` and `~/.local/state/portside` with their contents, and, for a state directory set with `PORTSIDE_STATE_DIR`, from that directory, its `logs` and the files Portside keeps in it (never from parent directories or other files). After a crash of the daemon itself, leftover process groups are verified by start time (read from `ps` in the C locale, so a daemon started under another locale still recognizes them) and stopped before servers are restored.

## Periodic checks

Process exits are noticed as they happen; crashes and restart policies need no polling. On a timer the daemon checks:

- Readiness: the port (and `health` path) of each starting server every 400 ms until it is running, then every second. A running server warns once a listener or health failure has lasted 4 seconds.
- Ports: every 2 seconds, a TCP connection to the port of each server that is not running, to show one taken by another process. Its owner is looked up with `lsof` (or `ss`) only when it is new.
- CPU and memory: one `ps` call for all servers, every 2 seconds while a client shows usage, otherwise every 30 seconds. An events client (`/api/events`) shows usage unless it connects with `usage=0` or sends `{"usage": false}`, and sends `{"usage": true}` when it shows usage again. The dashboard says so when its tab or window is hidden, and the menu bar app shows usage only while its panel is open. The first client that shows usage gets a new sample at once. The sparkline history holds 2-second samples and starts over after a longer gap. The daemon sends state to clients only when something changed.
- Listening addresses: one `lsof` call (or `ss`) when a server becomes ready, and every 30 seconds along with a usage sample while servers run, to tell which may be reachable from the network ([Security](security.md#servers-reachable-from-the-network)); container ports are checked with each container resync while a local engine publishes one.
- The config file's modification time, every second and before each request from a client.
- Containers: the Docker event stream, a full resync every 30 seconds and a stats stream per running container (see [Containers](containers.md)).
- While connected to another computer: a ping every 15 seconds and a check of the forwarded ports every 2 seconds.

Measured with `scripts/bench-idle.sh` on an Apple M4 Pro (macOS 27.0.1, about 690 processes) with 5 running `python3 -m http.server` servers out of 21 configured and no containers, over one minute each:

| Events clients | Daemon CPU | `ps` calls | `ps` CPU |
| --- | --- | --- | --- |
| none | 0.35 s | 2 | 0.04 s |
| one with `usage=0` (the menu bar app with its panel closed) | 0.42 s | 2 | 0.04 s |
| one showing usage (an open dashboard) | 0.55 s | 30 | 0.58 s |

With the app's panel closed that is about 0.8 % of one core; an open dashboard brings it to about 1.9 %. To measure on your machine, run `scripts/bench-idle.sh` (`--servers`, `--running` and `--window` change the setup; see [Development](development.md)).

Reading listening addresses adds about 2 `lsof` calls a minute while servers run and 2 more while running containers publish ports; on the same Mac with 7 such containers, the 3 to 4 calls took 0.11 to 0.13 s of CPU a minute in all three cases.

## Upgrades in place

`portside daemon restart` upgrades a running daemon in place: it asks the daemon to run the CLI's own binary, the daemon checks that binary (`<binary> version`) and then re-executes it in the same process. Servers stay its children and their terminals stay open, the listening sockets carry over (requests made meanwhile wait instead of failing), and launchd or systemd see no exit.

A handoff file in the state directory (`handoff.json`, removed by the new daemon) lists each server's process, start time, terminal and state; the new daemon takes each one over and resumes its output, and anything it cannot take over is ended and restored as after a crash. The command waits until the daemon answers with the new version; if none answers within 30 seconds, it starts a fresh daemon, which restores servers as after a crash. The daemon refuses while a server is stopping or a Compose job runs, and stays on the old binary if the new one does not run.

A daemon from before this feature, or none at all, is stopped and started instead, which restarts its servers. A daemon from before verified requests cannot prove itself to the CLI, which therefore sends it nothing signed; it is stopped with SIGTERM instead, once the CLI has checked that it is your account's process listening on the daemon port. The menu bar app shows such a daemon's version with a **Restart** button.

The menu bar app upgrades a daemon older than its bundled binary on its own when it starts or reconnects, as after `brew upgrade --cask portside`, which reopens the app it quit. It runs `portside daemon restart --in-place`, which only upgrades in place and fails instead of stopping the daemon, so servers never restart for it; it tries once per daemon version while it runs and shows nothing when it works. Only releases compare (the binary decides and reports `outdated` in `daemon info`): a development build, an equal version or a newer daemon is left alone. A daemon it could not upgrade keeps the **Restart** banner, with the reason. `portside daemon stop`, `portside service uninstall` and **Stop Daemon and Servers** in the app still stop every server, also when the stop arrives during an upgrade. Sharing and the connection to another computer resume as after any start.

## The macOS app

The macOS app (`macos/`, Swift) bundles the same binary in `Portside.app/Contents/Helpers/portside`, starts the daemon if needed and follows its event stream. Quitting the app leaves the daemon and your servers running; **Stop Daemon and Servers** in its menu stops them, and the panel and dashboard window then offer **Start**. When the config file does not load, the daemon keeps running on the last good settings and the app shows the error with **Edit**.

While the dashboard shows another computer, the panel says so with **Disconnect**; opening a server from the panel or a notification, or **Connect Coding Agents**, asks to disconnect first, since both are about this computer. A server whose port carries the other computer's forwarded server is labeled as such, and **Start** takes the port back.

**Settings** in its menu holds **Launch at Login** (registers the app as a login item), the app icon, **Install Command Line Tool** and **Connect Coding Agents** (see [Coding agents](coding-agents.md#setup)).

## Stopping on purpose

A deliberate stop (`portside daemon stop`, **Stop Daemon and Servers** in the app or the dashboard, `portside service uninstall`) leaves `~/.local/state/portside/stopped`. While it exists the app shows the daemon as stopped instead of starting it again; any explicit start removes it: the app's **Start**, `portside daemon start` or any command that needs the daemon.

## Login service

`portside service install` writes a launchd agent (`~/Library/LaunchAgents/app.foreverbeta.portside.plist`) or a systemd user unit. While it is installed, a daemon started on demand is started by it, and `portside daemon restart` uses it (`launchctl kickstart -k` or `systemctl --user restart`) when the daemon cannot be upgraded in place, so the service keeps supervising the daemon; an in-place upgrade keeps the service's process. It restarts a daemon that crashes, not one that was stopped.

On macOS, the app's **Launch at Login** is the recommended way to start the daemon at login; the service is for Linux and setups without the app, and `service install` points out when both are on.

On Linux, systemd stops user services at logout unless lingering is enabled (`sudo loginctl enable-linger $USER`); `service install` and `portside doctor` say when it is off. The unit is wanted by `default.target`, so it also runs without a desktop, and is ordered after `graphical-session.target` without being part of it. That ordering does not give the daemon a display: the desktop has to import its session variables into the user manager, which most desktops do at login (`systemctl --user import-environment` or `dbus-update-activation-environment --systemd` in their session startup; add one of them to your session startup if yours does not). `service install`, and every start of the service by the CLI, also copy `DISPLAY`, `WAYLAND_DISPLAY`, `XDG_RUNTIME_DIR`, `DBUS_SESSION_BUS_ADDRESS` and `XDG_SESSION_TYPE`, where set, into the systemd user manager, so notifications and opening an editor or terminal work; a `DISPLAY` forwarded over SSH (`ssh -X`, with `SSH_CONNECTION` set or a value such as `localhost:10.0`) is left out, since it ends with the SSH session. `portside doctor` warns when the service runs without a display while you are in a desktop session. Without systemd (Alpine, Void, Devuan, WSL without systemd), `service install` explains how to start the daemon from your shell profile (`portside daemon start`) or your init system instead.

## Agent sandboxes

A command run inside an agent's sandbox (Codex sets `CODEX_SANDBOX` or `CODEX_SANDBOX_NETWORK_DISABLED`, Claude Code's sandbox `SANDBOX_RUNTIME`) does not start a daemon, since the daemon and every server it starts would inherit the sandbox; the command and the MCP tools ask for `portside daemon start` in a normal terminal, or the app, instead. A daemon that runs already is used as usual; when it runs but the sandbox blocks connections to it, the message says so and asks for local network access instead. Elsewhere a daemon started on demand is a detached child process (or the login service when it is installed for the same config and state).
