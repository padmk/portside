# Other computers

Work on one computer while the projects run on another, for example a laptop driving a desktop. The dashboard connects to Portside on the other computer and manages its servers, containers, logs and terminals as if you sat at it, and its sites open in your local browser.

## Pairing

1. Install [Tailscale](https://tailscale.com/download) on both (on macOS also `brew install --cask tailscale-app`) and sign both in to the same Tailscale account. Until then the **Computers** window says what is missing: Tailscale not installed, not connected, or waiting for a sign-in.

A coding agent can do the whole setup with you: **Copy prompt for your agent** in the **Computers** window, or `portside computers --print-prompt`, gives a request that walks it through installing and connecting Tailscale, turning sharing on, pairing and checking the connection, asking you for the steps only you can do (signing in, enabling HTTPS certificates, the pairing code).

2. On the computer that runs the projects, turn sharing on: **Computers** in the top bar, then **Let my other devices connect**, or `portside share on` (it sets `share = true` in the config). Either shows a one-time pairing code, valid for 10 minutes.
3. On the computer you work on, open **Computers**, choose **Pair** next to the other computer and enter the code. Pairing is needed once per device; afterwards **Connect** is enough.

`portside share code` prints a new code, `portside share devices` lists paired devices and `portside share revoke <device>` unpairs one, which also ends its connection. The **Computers** window on the shared computer does the same.

The shared computer shows a desktop notification when a device pairs and when a paired device connects after being away for 5 minutes or more; reconnecting after a network change or a daemon restart is not announced again. Its **Computers** window, and `portside share devices`, show for each paired device whether it is connected now or when it last connected (kept in `share-devices.json`). Discovery from the other computer's **Computers** window does not count as connecting. The notifications follow the `notify` setting ([Configuration](configuration.md)); while the macOS app runs, it shows them.

## While connected

The window changes color and shows the other computer's name in the top bar. Everything in the dashboard then acts on that computer: servers, logs and terminals, containers, ports, adding servers and editing its config. Opening a folder, an editor or a terminal is not offered, because it would happen on the other computer's screen. **Disconnect** returns to this computer.

The connection belongs to the daemon, so every dashboard window follows it. It outlasts a daemon restart: the daemon reconnects to the same computer in the background when it starts (saved in `remote.json` in the state directory), until you disconnect or the other computer removes the pairing. When the other computer's daemon comes back on another automatic port, the connection follows it there.

From the terminal, `portside computers` lists your computers that share their servers, `portside pair <computer> <code>` pairs and connects, `portside connect <computer>` connects to a paired one and `portside disconnect` ends the connection. While connected, `--on <computer>` runs `status`, `up`, `down`, `restart`, `ensure`, `logs`, `attach` and `containers` there, for example `portside logs api --on devbox --grep error`; it fails when this computer is connected to another one or to none.

## Sites

Sites open as if they ran here:

- While connected, the port of each running remote server (and each published container port, such as a database) is forwarded to the same port on this computer, so `http://127.0.0.1:5173` and `psql -p 5432` reach the other computer. A port a local process listens on (on loopback or all addresses) is left alone and listed as busy, and forwarded as soon as it is free again (within a few seconds; connections still closing on it do not count); so is a forwarded port once a local process starts listening on it, for example a local `docker compose` publishing the same database port (noticed within a few seconds on macOS; Linux refuses that local bind while the port is forwarded). Starting a local server on a forwarded port takes the port back for as long as that server is starting or running, and a local server whose port is forwarded shows **forwarded from** that computer instead of a foreign process. For a busy port, **Use another port** in the **Computers** window forwards it to a local port you choose, for example 5432 to 15432, remembered for that computer. The switch next to each forwarded port, or `portside forward off <port>`, turns its forwarding off, also remembered for that computer: the port no longer listens here, its open connections end, and `<name>.<computer>.localhost` for a server on it shows that forwarding is off instead of the server. A port the other computer does not forward is refused, with the ports it does forward. `portside forward on <port>` or the switch turns it on again. Every port is forwarded until you turn it off, including ports published by containers of no configured project. When the connection is lost for about a minute, the forwarded ports are released until it is back. Like any local server, a forwarded port can be reached by every program and account on this computer ([Security](security.md#sharing)).
- Every remote server is also at `http://<name>.<computer>.localhost:7878`, for example `http://web.devbox.localhost:7878`. A link to `http://<name>.localhost:7878` copied from the other computer works too when this computer has no server of that name.

## Requirements

Only devices signed in to the same Tailscale user that are not tagged appear in the list. Sharing uses Tailscale Serve with HTTPS: the daemon publishes `https://<computer>.<tailnet>.ts.net:7878` inside your tailnet, which `tailscale serve status` lists, and withdraws it when sharing is turned off or the daemon stops. It refuses to replace a Serve entry on that port that it did not create. The tailnet must have HTTPS certificates enabled (Tailscale admin console, DNS page). On Linux, allow Serve for your account once with `sudo tailscale set --operator=$USER`. Discovery checks each online device on ports 7878 and this daemon's own port.

How paired devices are authenticated is described in the [security model](security.md#sharing).
