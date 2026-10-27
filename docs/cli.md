# CLI

Commands start the daemon when none runs, except those that only report on it: `check`, `daemon status`, `daemon stop` and `daemon info --no-start`. `portside` without a command runs `status` once a config file exists, and prints the help otherwise. `portside help <command>` prints a command's arguments.

Global flags go before the command:

- `--config FILE` uses another config file, like `$PORTSIDE_CONFIG` ([Configuration](configuration.md#where-things-live)).
- `--on <computer>` runs `status`, `up`, `down`, `restart`, `ensure`, `logs`, `attach` or `containers` on the connected computer ([Other computers](other-computers.md#work-on-the-other-computer)); it may also follow the command.

```text
portside status [--json]                 list servers with state, port and usage; a task
                                         shows task for its port and done after code 0, a
                                         schedule its next run (next 09:00 today,
                                         next Mon 09:00 within a week, else the date)
portside check [name|group]... [--json]  exit 0 when every server that should run (or each
                                         one named) is ready, 1 when one is not, 2 when the
                                         daemon cannot tell
portside up <name|group>... | --all      start, dependencies first; --wait waits until each
                                         is ready and prints its URL (done for a task),
                                         --takeover stops a foreign process holding the port
portside down <name|group>... | --all    stop gracefully; removes temporary servers
portside restart <name|group>...         restart; --wait and --takeover as for up
portside ensure <name> [--json]          start if needed, wait until ready, print the URL
                                         (a task runs again and must exit with code 0);
                                         --timeout 90s, --takeover as for up,
                                         --dir <checkout> runs it from another git worktree
portside logs <name> [-n 200] [-f]       print or follow a server's or container's output;
                                         --raw keeps terminal escape codes;
                                         --grep <regexp> [-C N] searches it, -t adds times,
                                         --since 10m (or an RFC 3339 time) limits it,
                                         none of these three with -f or --raw
portside attach <name>                   interactive terminal, Ctrl-] detaches
portside open [name] [--alias] [--print] open the dashboard or a server; --alias opens
                                         <name>.localhost, --print only prints the address
portside containers [server|project]... [--json]
                                         containers, their state and server
portside containers start|stop|restart <name>
portside containers recreate <name> [--build] [--pull]
                                         recreate, wait and print the output tail
portside ports [--json]                  every listening TCP port and its owner

portside add <name> --cmd "..." [--dir .] [--port N] [--group G]
                                         add a server; without --port a free port,
                                         --port 0 for none
portside rm <name>                       stop the server and remove it from the config
portside enable <name> | disable <name>  include or exclude a server in --all and restore
portside scan [dir] [--add [--yes]] [--json] [--depth 3]
                                         find projects under dir (default: the config's
                                         root); --add asks first on a terminal
portside init                            write the starter config file
portside config [path|edit|check]        print the config's path (default), open it in
                                         $VISUAL or $EDITOR, or validate it

portside daemon                          run the daemon in the foreground
portside daemon start|stop|status        start it in the background, stop it and its
                                         servers, or show whether it runs (exit 3 if not)
portside daemon restart [--in-place] [--allow-downgrade]
                                         upgrade in place, else stop and start;
                                         --in-place fails instead of restarting the servers,
                                         --allow-downgrade replaces a newer daemon
portside daemon info [--no-start]        JSON for the macOS app; with --no-start exit 3
                                         when no daemon runs; exit 4 when none runs and
                                         the config does not load (a running daemon keeps
                                         its last good config: exit 0 with configError)
portside daemon logs [-n 100] [-f]       print or follow its log, from the user journal
                                         when systemd runs it
portside service install|uninstall|status
                                         start the daemon at login (launchd, systemd)
portside share [on|off]                  show or switch sharing with your paired devices
                                         on Tailscale; on prints a pairing code
portside share code|devices|revoke <device>
                                         new pairing code, paired devices (connected now
                                         or when last connected), unpair one
portside computers [--json] | --print-prompt
                                         your other computers that share, and the
                                         connection; --print-prompt prints a setup
                                         request for your coding agent
portside pair <computer>                 pair with a computer using its code, read from
                                         standard input (typed or piped), then connect
portside connect <computer> | disconnect connect to a paired computer, or end the connection
portside forward on|off <port>           forward a port of the connected computer, or stop;
                                         kept for that computer
portside mcp [--mode read-only|start|full] [--dir <path>]...
                                         MCP server on stdio; --mode (default full;
                                         --read-only is read-only) limits what the tools
                                         change, --dir the projects they see
portside setup [--yes] [--json] [--mcp-mode <mode>]
                                         connect Claude Code and Codex (MCP server, skill);
                                         --mcp-mode registers the MCP server in that mode
portside setup --apply <id>[,<id>...] --yes
                                         apply only those items, print the results as JSON
portside setup --print-mcp | --print-prompt
                                         print the entry for other MCP clients, or a setup
                                         request for your coding agent
portside skill install [--force] | print install the agent skill for Claude Code and Codex
                                         (--force replaces a file it did not write), or
                                         print it
portside doctor [--fix]                  check the setup; --fix connects coding agents
portside version                         print the version (also --version)
```

[Reading state from scripts](linux-server.md#reading-state-from-scripts) covers `check`, `status --json` and the HTTP API. [Coding agents](coding-agents.md) covers `mcp`, `setup` and `skill`.

## Opening the dashboard

`open` uses the first command in `$BROWSER` that is on your `PATH` (commands separated by colons, `%s` standing for the address). Without one it uses `open` on macOS, `wslview` or `explorer.exe` under WSL, and `xdg-open` on a Linux desktop. A server's own address needs no sign-in; the dashboard does.

It prints the sign-in link instead of opening it when no browser is found, over SSH (`$SSH_CONNECTION` is set) or with `--print`. The link works once, within two minutes, and only from a process of your account.

Over SSH, forward the daemon's port, for example `ssh -L 7878:127.0.0.1:7878 <host>`, and open the link on your own computer, where `*.localhost` resolves to it. When that port is taken on your computer, forward another one (`-L 17878:127.0.0.1:7878`) and change the port in the link to match.

Under WSL the browser runs on Windows and reaches the daemon through the WSL network relay. The daemon may then fail to attribute the connection to your account and refuse the sign-in; run a browser inside WSL or reach the dashboard over an SSH tunnel instead.

## Stopping and readiness

`down` refuses to run without a name or `--all`. Stopping a server also cancels a pending automatic restart, which `status --json` shows as `restartPending`. Starting a server that is still stopping (`ensure` or `up` right after `down`) starts it again once it and every process it started have exited, unless another stop follows.

A server that is still starting after 10 seconds reports the port or health check it is waiting for. When its process tree listens on another port, `listenPorts` and the status note name that port.

A running server whose listener or health check keeps failing is shown as not responding ([Server states](dashboard.md#server-states)); a short gap, such as a hot reload, does not count. `ensure` checks readiness again even when the process runs. Health probes, direct URLs and the `*.localhost` proxy use whichever loopback address answers, IPv6 included.
