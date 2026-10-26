# CLI

```text
portside status [--json]                 list servers with state, port and usage
portside up <name|group>... | --all      start (dependencies first); --wait, --takeover
portside down <name|group>... | --all    stop gracefully; removes temporary servers
portside restart <name|group>...
portside ensure <name> [--json]          start if needed, wait until ready, print the URL;
                                         --dir <checkout> runs it from another git worktree
portside logs <name> [-n 200] [-f]       print or follow a server's or container's output;
                                         --grep <regexp> [-C N] searches it,
                                         -t adds times, --since 10m limits it
portside containers [server|project] [--json]  containers, their state and server
portside containers start|stop|restart <name>
portside containers recreate <name> [--build | --pull]  wait for recreation and print its output tail
portside attach <name>                   interactive terminal, Ctrl-] detaches
portside open [name] [--alias] [--print] open the dashboard or a server
portside ports [--json]                  every listening TCP port and its owner

portside add <name> --cmd "..." [--dir .] [--port N] [--group G]
portside rm <name> | enable <name> | disable <name>
portside scan [dir] [--add [--yes]]      discover projects; --add asks first on a terminal
portside init                            write the starter config file
portside config [path|edit|check]

portside daemon [start|stop|restart|status|logs]
portside service install|uninstall|status
portside share [on|off]                  let your paired devices on Tailscale connect;
                                         on prints a pairing code
portside share code|devices|revoke <device>   new pairing code, paired devices, unpair one
portside computers [--json]              your other computers that share, and the connection;
                                         --print-prompt a setup request for your coding agent
portside pair <computer> <code>          pair with a computer using its code, then connect
portside connect <computer> | disconnect connect to a paired computer, or end the connection
portside --on <computer> <command>       run status, up, down, restart, ensure, logs, attach
                                         or containers on the connected computer
portside mcp [--mode <mode>] [--dir <path>]...
                                         MCP server on stdio; --mode read-only|start|full (default
                                         full; --read-only is read-only) limits what the tools
                                         change, --dir the projects they see
portside setup [--yes] [--json]          connect Claude Code and Codex (MCP server, skill);
                                         --mcp-mode <mode> registers the MCP server in that mode,
                                         --print-mcp prints the entry for other MCP clients,
                                         --print-prompt a setup request for your coding agent
portside skill install|print             agent skill for Claude Code and Codex
portside doctor [--fix]                  check the setup; --fix connects coding agents
```

## Opening the dashboard

`open` uses the first command in `$BROWSER` (commands separated by colons, `%s` standing for the address), else `open` on macOS, `wslview` or `explorer.exe` under WSL, and `xdg-open` on a Linux desktop. Without a browser, over SSH (`$SSH_CONNECTION` is set) or with `--print`, it prints the sign-in link instead, which works once, within two minutes, and only from a process of your account.

Over SSH, forward the daemon's port with the same number, for example `ssh -L 7878:127.0.0.1:7878 <host>`, and open the link on your own computer, where `*.localhost` resolves to it. Under WSL the browser runs on Windows and reaches the daemon through the WSL network relay, so the daemon may not attribute the connection to your account and refuse the sign-in; then run a browser inside WSL or reach the dashboard over an SSH tunnel.

## Stopping and readiness

`down` and `stop` refuse to run without a name or `--all`. Stop also cancels a pending automatic restart. JSON status exposes that wait as `restartPending`.

After 10 seconds of startup, a server reports the port or health check it is waiting for. If its process tree listens on another port, `listenPorts` and the status note identify it. Running servers warn after a listener or health failure lasts 4 seconds, allowing brief hot reload gaps. `ensure` rechecks readiness even for a running process; health probes, direct URLs and the alias proxy use the loopback address that answers, including IPv6.
