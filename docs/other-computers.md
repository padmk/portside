# Other computers

Work on one computer while the projects run on another, for example a laptop driving a desktop. The dashboard connects to Portside on the other computer and manages its servers, containers, logs and terminals as if you sat at it, and its sites open in your local browser.

## Before you start

- Install [Tailscale](https://tailscale.com/download) on both computers (on macOS, or `brew install --cask tailscale-app`) and sign both in to the same Tailscale user. Only devices of that user that are not tagged can pair.
- Enable HTTPS certificates for the tailnet once (Tailscale admin console, DNS page).
- On Linux, allow Tailscale Serve for your account once: `sudo tailscale set --operator=$USER`.

Until Tailscale is ready, the **Computers** window says what is missing: Tailscale not installed, not connected, or waiting for a sign-in.

A coding agent can do the whole setup with you: **Copy prompt for your agent** in the **Computers** window, or `portside computers --print-prompt`, gives a request that walks it through installing and connecting Tailscale, turning sharing on, pairing and checking the connection, and asks you for the steps only you can do (signing in, enabling HTTPS certificates, entering the pairing code).

## Pair

1. On the computer that runs the projects, turn sharing on: **Computers** in the top bar, then **Let my other devices connect**, or `portside share on` (it sets `share = true` in the config). Either shows a one-time pairing code, valid for 10 minutes.
2. On the computer you work on, open **Computers**, choose **Pair** next to the other computer and enter the code, or run `portside pair <computer> <code>`. Pairing connects at once.

Pairing is needed once per device; afterwards **Connect** or `portside connect <computer>` is enough.

## Work on the other computer

While connected, the window changes color and shows the other computer's name in the top bar. Everything in the dashboard then acts on that computer: servers, logs and terminals, containers, ports, adding servers and editing its config. Opening a folder, an editor or a terminal is not offered, because it would happen on the other computer's screen. **Disconnect**, or `portside disconnect`, returns to this computer.

The connection belongs to the daemon, so every dashboard window follows it. It outlasts a daemon restart: the daemon reconnects to the same computer in the background when it starts, until you disconnect or the other computer removes the pairing. When the other computer's daemon comes back on another automatic port, the connection follows it there.

From the terminal, `--on <computer>` runs `status`, `up`, `down`, `restart`, `ensure`, `logs`, `attach` and `containers` on the connected computer, for example `portside logs api --on devbox --grep error`. It fails when this computer is connected to another computer or to none. `portside computers` lists your computers that share their servers and the connection. Local `portside status` with no servers here points to `--on`, and `portside doctor` reports sharing and the connection in one line each while they are in use.

Coding agents act on the connected computer through the `computer` argument of the server and container tools ([Coding agents](coding-agents.md#mcp-tools)).

## Forwarded ports

While connected, the port of each running remote server, and each port published by a running container (such as a database), is forwarded to the same port on this computer, so `http://127.0.0.1:5173` and `psql -p 5432` reach the other computer. Every such port is forwarded until you turn it off, including ports of containers that belong to no configured project.

- **Busy ports.** A port a local process listens on (on loopback or all addresses) is left alone and listed as busy, then forwarded within a few seconds once it is free again; connections still closing on it do not count. The same happens to a forwarded port once a local process starts listening on it, for example a local `docker compose` publishing the same database port: macOS notices within a few seconds, and Linux refuses that local bind while the port is forwarded.
- **Local servers.** Starting a local server on a forwarded port takes the port back for as long as that server is starting or running. A local server whose port is forwarded shows **forwarded from** that computer instead of a foreign process.
- **Another local port.** For a busy port, **Use another port** in the **Computers** window forwards it to a local port you choose, for example 5432 to 15432, remembered for that computer.
- **Turning a port off.** The switch next to each forwarded port, or `portside forward off <port>`, stops forwarding it, remembered for that computer: the port no longer listens here, its open connections end, and `<name>.<computer>.localhost` for a server on it says that forwarding is off. `portside forward on <port>` or the switch turns it on again. A port the other computer does not forward is refused, with the ports it does forward.
- **Lost connection.** When the connection is down for about a minute, the forwarded ports are released until it is back.

Like any local server, a forwarded port can be reached by every program and account on this computer ([Security](security.md#sharing)).

## Sites

Every remote server is also at `http://<name>.<computer>.localhost:7878`, for example `http://web.devbox.localhost:7878`. A link to `http://<name>.localhost:7878` copied from the other computer works too when this computer has no server of that name.

## Manage paired devices

On the shared computer, `portside share code` prints a new code, `portside share devices` lists paired devices and `portside share revoke <device>` unpairs one, which also ends its connection; its **Computers** window does the same. `portside share off` turns sharing off.

The shared computer shows a desktop notification when a device pairs, and when a paired device connects after being away for 5 minutes or more; reconnecting after a network change or a daemon restart is not announced again. Its **Computers** window and `portside share devices` show for each paired device whether it is connected now or when it last connected. A scan from the other computer's **Computers** window does not count as connecting. The notifications follow the `notify` setting ([Configuration](configuration.md#example)); while the macOS app runs, it shows them.

## Troubleshooting

- **The other computer is not listed.** Both must be online in Tailscale under the same user, the device must not be tagged, and sharing must be on there. Discovery tries the default port 7878, this daemon's own port and the automatic fallback ports ([Configuration](configuration.md#the-daemons-port)).
- **Sharing is on but not active.** `portside share` shows the reason. Usually HTTPS certificates are not enabled for the tailnet, or on Linux the operator setting is missing (see [Before you start](#before-you-start)); the daemon retries every minute. It also refuses to replace a Tailscale Serve entry on its port that it did not create.
- **The pairing code is refused.** A code works once, for 10 minutes, and is withdrawn after 10 wrong guesses. Get a new one with `portside share code` on the shared computer.
- **The clocks differ.** Signed requests are accepted within two minutes of the other computer's clock; fix the clock of the computer that is off.
- **The other computer does not recognise this one, or cannot prove it is the one you paired with.** The pairing was removed there, or something else answers at its address. Pair again with a new code.

## How sharing works

Sharing uses Tailscale Serve with HTTPS: the daemon publishes `https://<computer>.<tailnet>.ts.net:<port>` (its own port, 7878 by default) inside your tailnet, which `tailscale serve status` lists, and withdraws it when sharing is turned off or the daemon stops. The connecting computer keeps the saved connection in `remote.json` in its state directory, and paired devices with their last-seen time are kept in `share-devices.json` on the shared one.

How requests are authenticated, and what a paired device may do, is in the [security model](security.md#sharing).
