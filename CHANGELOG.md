# Changelog

## Unreleased

- **`watch`:** a server with many watched files is scanned less often instead of costing more, about 2 % of one core at most for all servers together, and a large tree slows only its own server; a change among 20,000 files takes a few seconds to notice ([Periodic checks](docs/how-it-works.md#periodic-checks)). `**` also skips `venv`.
- **`portside check`** counts a task as healthy while it runs, so a long scheduled task no longer fails a monitor's check; a task you name must still be done.
- **Fixed:** a stop asked for while the daemon upgrades in place (about a second) is refused with a message to try again, instead of being lost or counted as a crash by the new daemon. Two servers that need the same task, started at once, no longer run it twice.

## 0.10.0

- **Tasks:** `oneshot = true` makes an entry a task that runs to completion, such as a migration. Servers that `depends_on` it start once it exits with code 0, and are `crashed` with a note such as `dependency migrate failed: exited with code 1` when it fails. `ensure`, `up --wait` and `ensure_server` run the task again and wait for its end; servers started together share one run. A task has no port, shows as **done** after a successful run, and counts for `portside check` only while it runs, when it failed, or when you name it ([Tasks](docs/configuration.md#tasks)).
- **Schedules:** `schedule = "30 2 * * *"` starts a task or server at the times a crontab expression matches, in local time (five fields, names, ranges, lists, steps and `@daily` style macros). A run missed during sleep or while the daemon was down happens once afterwards, a server still running skips the run, `PORTSIDE_TRIGGER` is `schedule`, and `enabled = false` pauses it. `portside status`, the dashboard and `list_servers` show the next run ([Schedules](docs/configuration.md#schedules)).
- **`memory_limit`** (top level or per server, such as `"2GB"`; `"0"` turns an inherited one off) stops a server whose processes use more memory, checked with each usage sample (every 30 seconds, every 2 seconds while the dashboard shows usage). It counts as a crash with a note such as `used 2.3 GB, over memory_limit 2 GB`: the restart policy applies and `on_crash` gets the note. Worktree copies have their server's limit; containers are not covered ([Memory limit](docs/configuration.md#memory-limit)).
- **`watch = ["src/**/*.py", "pyproject.toml"]`** restarts a server once changes to the files it matches, relative to its `dir`, have stopped for about a second, and starts a crashed one again after a change, so fixing the error brings it back. The log names the change (`restarting: changed src/app.py (+3 more)`) and `PORTSIDE_TRIGGER` is `watch`. A server you stopped ignores changes, and a task restarts only while it runs. Files are polled every second, only in directories a pattern can match, skipping `.git`, `node_modules` and similar folders unless a pattern names them. The dashboard marks such a server **watching** ([Restart on file changes](docs/configuration.md#restart-on-file-changes)).
- **Hook commands:** `PORTSIDE_TIME` gives when the event happened (RFC 3339), so a monitor can order reports whose commands finished out of order.
- **Fixed:** an automatic restart still waiting for its backoff when the daemon upgraded in place was lost with `restore = false`, or happened at once with a fresh count of restarts; it now happens at its time in the new daemon and continues the count, unless the config the new daemon loads sets `restart = "no"`.
- **Fixed:** a server that was still starting when the daemon upgraded in place no longer stays down with `restore = false`, nor when the upgrade fails: the start goes on.

## 0.9.0

- **Hook commands:** `on_unready` runs when a running server has not answered on its port or `health` path for `unready_after` (a minute unless set), and `on_recover` when a server that `on_crash` or `on_unready` reported is ready again, with `PORTSIDE_RECOVERED_FROM` and `PORTSIDE_DOWNTIME`. Like `on_crash`, both are set at the top level or per server, and one script can tell the events apart by `PORTSIDE_EVENT`.
- **Crash command:** `PORTSIDE_STREAK` counts the crashes for good in a row without the server being ready in between, so a monitor that starts failed servers again can report each problem once, and `PORTSIDE_TRIGGER` says what began the run (`start` or `restore`). The streak and an open problem survive daemon restarts and upgrades.
- **`umask`** (top level or per server) sets the mode mask for the files a server creates, such as `"0007"`; without it servers keep the daemon's, which launchd and systemd set to `0022`, readable by other accounts.
- **`login_env = false`** gives servers the daemon's own environment plus `path` and `env` instead of the login shell's, for servers whose environment the config alone should decide. `portside doctor` says which one servers get.
- **`retry_after`** (top level or per server, for `restart = "on-failure"` or `"always"`) starts a server again that long after its automatic restarts gave up or its start failed, with another 5 restarts, until it runs or you stop it; `PORTSIDE_TRIGGER` is then `retry`. The dashboard offers **Stop** while it waits, and the menu bar app still announces the crash.
- **Docs:** the Linux server guide explains that `systemctl --user restart portside` stops every server, while `portside daemon restart` keeps them running.

## 0.8.0

- **MCP `--dir` scope:** a container counts as a scoped server's only through the server's `projects` in the config or a Compose working directory inside the scope, so a project's `supabase/config.toml` naming another project's ID no longer exposes that project's containers; a Supabase CLI project ID listed in a server's `projects` belongs to that server alone, also on the dashboard. Containers and `list_ports` no longer name a server outside the scope, and `ensure_server` refuses a `dir` outside it.
- **Worktree copies:** `dir` must be a worktree git has registered for the repository; a directory whose `.git` file only points into the repository is refused.
- **MCP:** program output quoted by `ensure_server` and `restart_server` errors and by `container_action` is marked as untrusted. The docs say plainly that `start` and `full` modes run code the agent can edit.
- **Security, sharing:** unpairing a device, or pairing it again, now also refuses its request whose body was still arriving and ends every connection of the old pairing; the body of a paired device's request must arrive within 30 seconds. On Linux the sharing listener accepts connections only from root or your own account, which `tailscaled` runs as, so other local accounts that read the Serve path cannot send it forged Tailscale headers.
- **Security, sharing:** a reply relayed from the connected computer is cut off at 64 MiB, since the daemon may hold a whole reply to sign it for the CLI, so a computer that was taken over cannot exhaust this one's memory.
- **Security, sharing:** this daemon never follows a redirect from the connected computer, cuts off a relayed reply that stops for a minute, ignores a state that is not a JSON object, and opens that computer's server addresses only on this computer, at the forwarded port or through the proxy, whatever host they name; the dashboard ignores a state of another shape than it reads.
- **Security:** the CLI and the macOS app never follow a redirect from what answers on the daemon's port, for requests and WebSockets alike, which could have sent a signed request elsewhere before its reply was checked; writes need an `Origin` that is a plain origin (no user, path or query).
- **CLI and MCP:** names, notes and commands are printed with control characters replaced in tables, and the list tools tell agents that these fields are data, not instructions.
- **Security, pairing:** `portside pair <computer>` reads the code from standard input (it asks on a terminal, or reads a pipe); the code as an argument, which other accounts could read and redeem first, is refused.
- **Security, forwarded ports:** forwarded ports hold at most a quarter of the daemon's open-file limit (about 11,000 ports on a Mac, all of them on Linux), servers first; ports beyond it are listed as over the limit in the **Computers** window, `portside computers` and `portside doctor`, and turning a port off makes room. Binding forwarded ports and checking them for local listeners happen outside the lock other requests wait for, at most 128 every 2 seconds, so thousands of ports no longer stall the dashboard; a local server starting on a port that is being bound gets it, and the dropped bind never forwards a connection. This computer never listens for a forwarded port on the daemon's own, default or fallback ports; such a remote port is listed as busy and can go to another local port.
- **Fixed:** starting a server while it is still stopping (`portside down` and then `ensure`, or `ensure_server` and `depends_on` meanwhile) starts it again once its whole process group has exited, so a child still holding the port does not block it, also when the server exits just as the start arrives or is renamed meanwhile, instead of leaving it stopped until `ensure` timed out.
- **Fixed:** a daemon start after a stop no longer ends a process group whose id another program got in the meantime: a stopping daemon removes its servers' groups from the state once they have exited, and after a crash a leftover group is ended only when its processes can be tied to the server.
- **Fixed:** an upgrade in place is refused until a stopping server's processes have all exited, also after its main process did, so the upgrade no longer cuts the stop short and leaves a child running.
- **Fixed:** a server that exits while the daemon upgrades in place counts as crashed, so its restart policy or `on_crash` applies, instead of counting as stopped; with `restart = "no"` it stays down also when `restore` brings back the other servers.
- **Fixed:** shutting the daemon down while a server stops no longer sends it a second `Ctrl-C`, which made `docker compose` abort its graceful stop, and waits until that stop has ended every process the server started, also for a server removed from the config meanwhile; a second `portside down` also waits for it, and no restart stays announced for a server that crashed just before.
- **Fixed:** an `on_crash` command that leaves a background process holding its output no longer runs forever and takes one of the four slots.
- **Fixed:** `ready_log` and plain logs strip color sequences with `:` parameters, such as `\x1b[4:3m` (curly underline), so an anchored `ready_log` matches.
- **Fixed:** a state file that cannot be written (for example a read-only state directory) is reported in the daemon log and as a warning in the dashboard and `portside doctor`, instead of silently losing which servers you stopped.
- **`portside daemon info`** includes `configError` also when a running daemon goes on with the last config that loaded.
- **Fixed:** turning a server on or off in the config keeps a comment on the `enabled` line, edits keep the blank lines that end the file, a key written in another case (`Enabled`) is edited in place, and a server table with two keys that differ only in case (`cmd` and `Cmd`) is refused instead of loading either.
- **Linux is a first-class platform** (x86-64, arm64), next to macOS: CI runs the systemd user service end to end and an upgrade from the latest release on every change, and the README installs it with the same Homebrew tap (`brew install --formula padmk/tap/portside`) or from the release archives.

## 0.7.2

- **Dashboard, menu bar and CLI:** a running server that stopped answering on its port or `health` path shows as **not responding** (an amber diamond in the dashboard, an orange light in the menu bar, a yellow dot in `portside status`) and is counted apart from the running ones, as `portside check` already reported it.
- **Upgrades in place:** the upgrade is refused, and the servers keep running under the current daemon, when the terminal of a server still waiting for its `ready_log` line cannot be paused, so that line cannot slip between the two daemons; for other servers it is only logged.
- **CI:** the Linux service run also upgrades the daemon while a server is still waiting for its `ready_log` line, and both Linux and macOS upgrade the latest published release in place to the new build (`scripts/upgrade-from-release.sh`).

## 0.7.1

- **`portside check <name|group>...`** requires exactly the servers named, also after a stop, so a monitor can list what must run; an unknown or empty name exits 2 instead of passing.
- **`portside check`** also reports a running server that stopped answering on its port or `health` (after 4 seconds), with the reason, instead of counting it as ready.
- **Readiness:** a server still waiting for its `ready_log` line keeps waiting through an upgrade in place (`portside daemon restart`), instead of counting as ready at once, and a line the upgrade cut in two still counts as one; a server that had stopped answering stays reported as such after the upgrade. The line is matched as the output is written, so a flood of output can no longer hide it, and only once it is whole, so `^Worker ready$` no longer matches the start of `Worker ready not yet`.
- **Upgrades in place:** output a server writes while the daemon upgrades waits in its terminal for the new daemon, which reads it, so a `ready_log` line written at that moment is not missed.
- **Crash command:** `on_crash` also runs when a server cannot start because another program holds its port.
- **CLI:** `portside doctor` warns about notifications also when the daemon's `DISPLAY` or `WAYLAND_DISPLAY` is set but empty.

## 0.7.0

- **`portside check`** for monitoring: exit 0 when every server that should run is ready, 1 when one is not (crashed, given up, starting or restarting), 2 when the daemon is not running or cannot tell; `--json` gives each server and the reason. It never starts a daemon, so a systemd timer or cron job can run it without the API token ([Linux server](docs/linux-server.md#reading-state-from-scripts)).
- **Readiness:** `ready_log` keeps a server `starting` until a line of its output matches the expression, and `ready_after` until it has run that long; servers that `depends_on` it wait as well. A worker without a port can now say when it is ready.
- **Changed:** `health` needs a success or a redirect (status 200 to 399) for a server to count as running; other answers below 500, such as 404 or 401, no longer do. Point `health` at a path that answers 200 if a server now stays `starting`.

## 0.6.3

- **CLI:** `portside daemon logs` reads the user journal also when the systemd service failed to start, which is when its reason is needed, and reads the log file for a daemon of another state directory than the service's. Stopping such a daemon no longer says the login service starts it at login.
- **CI:** every push runs the Linux login service end to end on the runner's systemd: restarts and crash commands, the journal, an upgrade in place, a start that fails and an isolated instance (`scripts/linux-service-e2e.sh`).

## 0.6.2

- **Login service:** `portside service install` records the binary by its link on `PATH` (such as Homebrew's `bin/portside`) instead of the versioned file behind it, so the service still starts after `brew upgrade` removes the old version. Run `portside service install` again once to update an existing service.

## 0.6.1

- **Homebrew on Linux:** `brew install --formula padmk/tap/portside` builds the command-line tool and dashboard on Linux too, and its notes mention `portside service install`.
- **CLI:** `portside doctor` on Linux also warns when `notify-send` is installed but the daemon runs without a display, as on a server, since a crash of a server without `on_crash` would then notify no one.

## 0.6.0

- **Crash command:** `on_crash` (top level, or per server to override it; `""` turns it off for one) runs a command once a server has crashed for good: at once with `restart = "no"`, after the last automatic restart otherwise (with `always` also for a server that keeps exiting with code 0), and when a server cannot start. The server, exit code, uptime, restarts, note, log file and last 20 log lines come in `PORTSIDE_*` environment variables, never in the command line. It runs whatever the `notify` setting and connected apps, so a server without a desktop can report crashes ([Crash command](docs/configuration.md#hook-commands)).
- **Linux server:** a guide for a machine without a desktop: the systemd user service, restart policies, crash reports, reading state from scripts, coding agents and the dashboard over an SSH tunnel ([Linux server](docs/linux-server.md)).
- **CLI:** `portside daemon logs` prints the last 100 lines and exits, as `portside logs` does; `-n` sets the count and `-f` follows the log, which it used to do always. While systemd runs the daemon, it reads the user journal, where its output goes, and errors that point to the daemon's log name `portside daemon logs` instead of the file.
- **Dashboard over SSH:** through a tunnel to another local port (`ssh -L 17878:127.0.0.1:7878`), the dashboard's changes and live updates work too; requests from the page's own origin are accepted whatever port it is on.
- **Releases:** a release also waits for the commit's CodeQL scan to pass, and the app is checked as users get it: the zip unpacked, its signature verified and its command run.
- **CLI:** `portside doctor` says how many servers have an `on_crash` command, and on Linux warns when `notify` is on but `notify-send` is missing, since a crash would then notify no one.

## 0.5.0

- **macOS app:** when the daemon is a newer release than the app, the panel says to update the app instead of offering a restart that would downgrade it.
- **Updating is `brew upgrade`:** the menu bar app upgrades a running daemon older than itself in place when it starts or reconnects (Homebrew reopens the app it quit for the upgrade), so servers keep running and no `portside daemon restart` is needed. It tries once per daemon version; a daemon it cannot upgrade in place keeps the **Restart** banner, now with the reason.
- **CLI:** `portside daemon restart --in-place` only upgrades in place and fails instead of restarting the servers. `portside status` and `portside doctor` say when the daemon is older than the command; `daemon info` reports it as `outdated`.

- **Other computers:** a forwarded port is available again at once after the other computer's server goes down and up, after `portside forward on`, a reconnect or a local listener exiting, instead of staying busy here for up to 40 seconds while its closed connections linger (TIME_WAIT). A local listener on the port still takes precedence.
- **Config:** removing the last server of the config file (`portside rm`, the dashboard) no longer leaves a blank line at the end of the file.
- **CLI:** `portside up` right after `portside add` (or `rm`, or an edit in your editor) sees the change: the daemon checks the config file before each request instead of only once a second, so `unknown server or group` no longer appears for a server just added.
- **CLI:** `portside daemon restart` from an older release than the running daemon refuses instead of replacing it, and the daemon refuses such a binary too; `--allow-downgrade` replaces it anyway, and the command and the daemon log then say `downgraded` instead of `upgraded`. Development builds are not compared.
- **CLI:** the `portside computers` hint lists every command `--on` runs (also `attach` and `containers`), worded as in the help and the usage error.
- **Other computers:** `portside forward off <port>` for a port the connected computer does not forward fails and lists the forwarded ports instead of storing the setting; a forwarded port that is busy here can still be turned off.
- **Other computers:** replies relayed from the connected computer carry `X-Content-Type-Options` once instead of twice.
- **CLI:** on a computer connected to another one, `portside status` with no local servers points to `portside --on <computer> status`, and `portside doctor` reports sharing and the connection in one line each while they are in use.

## 0.4.3

- **CLI:** a command the running daemon does not know yet, after an update that has not restarted it, says so and suggests `portside daemon restart` instead of `no such endpoint`.
- **Releases:** a release is published only for a commit of `main` whose CI passed, after the built binary and the app's command have been run; published releases are immutable. CI also audits the dashboard's production dependencies.

## 0.4.2

- **May be reachable from the network:** the mark (and `portside status`, `doctor`, the menu bar panel) says a listener may be reachable, since its addresses do not show what the firewall lets in. The fix fits the tool the command runs, following package scripts (Uvicorn, FastAPI, Flask, Django, Gunicorn, Hypercorn, `http.server`, Astro, Nuxt, Remix, React Router, Rails and Phoenix included), and a Compose fix keeps the container's own port.
- Only this machine's own Tailscale addresses count as `tailnet`; other addresses in `100.64.0.0/10` (also carrier-grade NAT space) count as `network`. `portside doctor` lists published container ports whose exposure it could not check.

## 0.4.1

- **Security:** replies relayed from a connected computer also lose their `Location` header, so none of them can redirect the dashboard, as the security docs state.

## 0.4.0

- **Releases:** the files are built in jobs that cannot publish; a separate job creates the release, and the Homebrew tap token is only available to release tags in a protected environment.
- **Security:** replies relayed from a connected computer are served with a sandboxing Content-Security-Policy and without cookies, site data or redirect headers, so a taken-over computer you connect to cannot run script on this dashboard's origin (for example through a server icon opened in a new tab).
- **macOS app:** links from the dashboard open in the browser only when they are http or https.
- **CLI:** `portside scan --add` asks before adding when run on a terminal (`--yes` skips the question); scripts and agents are unaffected.
- **Other computers:** a switch next to each forwarded port in the **Computers** window, or `portside forward off|on <port>`, turns its forwarding off and on, remembered per computer. A port turned off stops listening, its open connections end, and `<name>.<computer>.localhost` for its server says forwarding is off. Every port is still forwarded by default.
- **Sharing:** the shared computer notifies you when a device pairs and when a paired device connects after being away, and its **Computers** window and `portside share devices` show whether each paired device is connected now or when it last connected.
- **Reachable from the network:** a running server or published container port that listens on all interfaces or another non-loopback address (`"5432:5432"` in Compose, `next dev`, `vite --host`, `app.listen(port)`) is marked **Reachable from network** in the dashboard, with how to bind it to `127.0.0.1` for its tool, in `portside status` and `portside doctor`, and as `exposure` and `networkWarning` in the API and `list_servers`. Tailscale-only listeners are not flagged. `lan = true` in a server's table says it is meant to be reachable and hides the mark for it and its containers ([Security](docs/security.md#servers-reachable-from-the-network)).
- **Agents:** with `portside mcp --dir`, `ensure_server` reuses a server that already runs even when a dependency outside the scope is stopped, since nothing would be started; `restart_server` still refuses then.

## 0.3.1

- **Agents:** with `portside mcp --dir`, `ensure_server` and `restart_server` no longer start servers outside the scope through `depends_on`: when such a dependency does not run, they refuse and name it, so the agent asks you to start it.

- **Benchmark:** `scripts/bench-idle.sh` measures the daemon's idle CPU and `ps` calls with no client, the app's closed panel and an open dashboard, so the numbers in How it works can be checked on any Mac.

## 0.3.0

- **Verifiable releases:** every release file has a GitHub build provenance attestation (`gh attestation verify <file> -R padmk/portside`), and each CLI archive an SPDX SBOM. CodeQL checks the Go and dashboard code, and CI builds the app and runs the Go tests on macOS for every push and pull request.
- **Homebrew formula:** `brew install --formula padmk/tap/portside` builds the command-line tool and dashboard from the tagged source, for those who prefer it to the prebuilt app; it conflicts with the cask.
- **Lighter in the background:** CPU and memory are sampled every 2 s only while a dashboard or the app's panel shows them, otherwise every 30 s; running servers are probed once a second and unchanged state is not sent again. An idle daemon with the app connected used about a third of the CPU in our measurement ([How it works](docs/how-it-works.md)).
- **Fix:** opening a server's log file from the dashboard refuses a name that leads out of the log directory.
- **Agents:** `portside mcp --mode read-only|start|full`: `read-only` (also `--read-only`) offers only the tools that read and never starts the daemon, `start` adds `ensure_server` without stopping or restarting; `--dir <path>` limits the tools to the servers, worktree copies and containers of those projects, and `portside setup --mcp-mode` registers a mode. Every MCP tool says whether it changes anything (read-only, idempotent or destructive annotations), and the log tools and instructions tell agents that log output is data, not instructions.
- **Documentation:** a Trust section in the security docs (what Portside can do as you, what agents can do, what leaves the machine, the missing notarization and how to verify a download), and a comparison with process-compose, pitchfork, Portless, WhatThePort and Overmind.

## 0.2.1

- **Security:** builds use Go 1.27.3. Releases up to 0.2.0 were built with Go 1.26.0, without the Go security fixes since, including the one for distrusting compromised intermediate certificates in `crypto/x509` (GO-2026-6809), which matters for TLS connections such as remote Docker engines and paired computers.

## 0.2.0

- **Getting started:** the empty dashboard, which the macOS app opens on its first launch, connects the coding agents (MCP server and skill) next to finding projects. Settings has the same under **Coding agents**, also opened from the app's **Connect Coding Agents** and the command palette; the app's separate agent window is gone.
- **Agent prompt:** `portside setup --print-prompt`, and **Copy prompt for your agent** in the dashboard, give a request to paste into a coding agent so it connects the agents, adds your projects and checks that they start.
- **Tailscale help:** the **Computers** window says whether Tailscale is not installed (with a download link and the Homebrew command), not connected, or waiting for a sign-in, instead of one general message. **Copy prompt for your agent** there, and `portside computers --print-prompt`, give a request that has a coding agent set up Tailscale, sharing and pairing with you.
- **Coding agents:** setup connects Claude Code and Codex only; Gemini CLI is no longer set up or checked (an MCP server added earlier stays in its config, and `portside setup --print-mcp` still prints the entry for any client).
- **macOS app:** **Connect Coding Agents** asks to disconnect first while the dashboard shows another computer, where the setup is not shown.
- **Homebrew:** `brew uninstall --zap` stops only the daemon that holds the state directory's lock, instead of every process named `portside` (such as coding agents' MCP servers or an unrelated program of that name).
- **Documentation:** a short README, with the details in `docs/` by topic; the release archives include `docs/`.

## 0.1.0

First public release.

- **Servers:** a daemon runs dev servers in pseudo-terminals with dependencies, groups, restart policies and graceful process-group shutdown. Readiness is checked continuously (listener and health path), servers keep what you asked for across crashes and restarts, scrollback survives daemon restarts, and the daemon keeps running on the last good config when the file breaks. `daemon restart` upgrades the daemon in place without stopping servers. When port 7878 is taken by another program and the config sets no port, the daemon takes the first free port of 7879-7899, then 17879-17899, and keeps it in the state directory (`auto-port`) so it stays the same; it refuses to move when another of your daemons holds the port.
- **Environment:** servers get the login shell's environment and start inside their project's tool environment (direnv, devbox, mise, fnm or nvm, detected from the server's directory); the `wrap` key turns this off or sets a prefix such as `nix develop -c`.
- **Worktrees:** a configured server runs as a temporary copy (`<name>--<worktree>`) from another git checkout, on its own port, and goes away with the worktree.
- **Discovery:** `scan` and the dashboard propose servers for Node frameworks and workspaces, Python (uv, Poetry, Pipenv, PDM or a virtualenv), Laravel, Phoenix, Spring Boot, ASP.NET Core, Go, Rust, Deno and Compose, with a port only for commands known to take one. The starter config's root is the projects folder that holds the most projects.
- **Dashboard:** cards and a list view, live terminals with search across all kept output, CPU and memory history, ports, a config editor that keeps comments and a command palette. Open in editor and Open in Terminal use the installed apps, or the ones picked in Settings. Six theme families in light and dark; English and Polish.
- **Containers:** the Docker Engine behind the active context (Docker Desktop, OrbStack, Colima, Rancher Desktop, Linux, rootless Docker, Podman, and remote `tcp://` and `ssh://` contexts) is followed live. Compose containers attach to their servers; logs, a shell, start, stop, restart, recreate and rebuild per container, stack controls per project, warnings and notifications when a container is unhealthy or fails, and Start Docker when the engine is down.
- **Other computers:** share a daemon with your other devices over Tailscale (paired with one-time codes, every request signed) and manage that computer's servers from this one, with their ports forwarded here.
- **Coding agents:** an MCP server (`portside mcp`) for Claude Code, Codex and Gemini CLI, with `setup --print-mcp` printing the entry for other MCP clients, and a skill for Claude Code and Codex: list, ensure, restart and stop servers, read and search server and container logs with times, act on containers. `portside setup` and `doctor --fix` add what is missing; the macOS app offers the same in its Connect Coding Agents window on first launch.
- **CLI:** `up`, `down`, `restart`, `ensure`, `logs` (with `--grep`, `-C`, `--since`), `attach`, `containers`, `scan`, `add`, `share`, `computers`, `setup`, `skill`, `doctor` and more. `portside open` honors `$BROWSER`, works under WSL, and over SSH or without a browser prints the sign-in link.
- **`*.localhost` proxy** with WebSocket support.
- **macOS menu bar app** (macOS 26 or later, Apple Silicon): status icon, project icons, quick actions, crash and container notifications, a dashboard window and launch at login. Linux is a preview: the CLI, daemon and dashboard in the browser run there with a systemd user service, but are less tested.
- **Security:** loopback only, Host and Origin checks, a per-user API token, one-time sign-in links for browsers and sandboxed project icons.
