# Security model

The daemon runs arbitrary commands from your config, so only your account may reach its API. To report a vulnerability, see [SECURITY.md](../SECURITY.md).

## Trust

- **It acts as you.** The daemon runs the commands in your config (servers, also at the times of their `schedule`, and hook commands such as `on_crash`) with your account's permissions, in no sandbox. Treat a server definition like a script you run yourself.
- **Nothing leaves the machine on its own.** Portside sends no telemetry and checks for no updates. The daemon listens only on loopback. It connects out only to the Docker engine of your active Docker context and, over Tailscale, to your own computers when you look for them, pair, connect or turn on sharing.
- **Coding agents get the MCP tools, not a shell.** Through MCP an agent can read every project's servers, containers, logs and listening ports, start, restart and stop configured servers (including temporary copies in a git worktree), act on containers, and do the same on a computer this one is connected to. It cannot run commands of its choosing, edit the config or remove volumes through MCP. A server's command does run the project's code, though, which the agent can edit: in `start` and `full` mode, starting or restarting a server runs what the agent changed, so only `--mode read-only` keeps an agent from running code it changed. `--mode read-only`, `--mode start` and `--dir <path>` narrow the tools ([Coding agents](coding-agents.md#modes-and-project-scope)); under `--dir`, a container counts only through a server's `projects` in the config or a Compose working directory inside the paths, never through an ID a project file names, and a worktree copy needs a registered git worktree inside the paths. An agent that also has a shell can run the CLI anyway, so this limits the tools, not the agent.
- **What it stores.** The config can hold secrets in `env`. The state directory holds the API token, the pairing keys and the server logs, which keep whatever your servers print, secrets included. Files Portside writes are readable only by your account (files your servers create follow their [umask](configuration.md#file-permissions)), and on every start the daemon removes group and other access from the default config and state directories and everything in them. For a config or state directory elsewhere, it tightens only the config file, the state directory itself and the files Portside keeps there; parent directories and unrelated files keep their modes. The locations are in [Configuration](configuration.md#where-things-live).
- **The macOS app is not notarized.** It is signed ad hoc, not with an Apple Developer ID, so Apple has not scanned it, and the Homebrew cask removes the quarantine flag so that Gatekeeper does not block the first launch. What you rely on instead is that the source is public and each release is built from a tagged commit by the [release workflow](../.github/workflows/release.yml) on GitHub Actions.
- **Checking a download.** From 0.3.0, every release file (the app zip, the CLI archives, their SBOMs and `checksums.txt`) carries a build provenance attestation that ties it to that workflow run and commit: `gh attestation verify Portside-<version>.zip -R padmk/portside`. To avoid the prebuilt app altogether, build from source, by hand or with the Homebrew formula that compiles the tagged source on your Mac (README, "Other ways to install").
- **Removing it.** `brew uninstall --zap --cask portside` stops the daemon and its servers and trashes the config and state. It leaves the coding-agent registrations: remove them with `claude mcp remove --scope user portside` and `codex mcp remove portside`, and delete the `portside` skill folder in `~/.claude/skills` and `~/.agents/skills` (or `$CODEX_HOME/skills`).

## Loopback only

The daemon listens on `127.0.0.1` and, wherever the system has IPv6 loopback, on `[::1]`. Browsers try `::1` first for `*.localhost`, so a program holding `[::1]:<port>` would receive every dashboard request with its cookie. The daemon therefore refuses to start while another program holds that address and names it in the error, and `portside doctor` checks that the daemon itself answers on both. Clients dial `127.0.0.1` by address.

## Signed requests

Every API call needs the token in `auth-token` in the state directory (mode `0600`, created on first start). Clients never send it; they sign with it. Each daemon start picks a random epoch.

1. **Handshake.** Before its first signed request, the CLI, the MCP server and the macOS app send `GET /api/health` with a random challenge (`X-Portside-Challenge`). The daemon answers with its epoch and a proof, an HMAC-SHA256 keyed by the token over the challenge and the epoch (`X-Portside-Epoch`, `X-Portside-Proof`). A client that gets no valid proof sends nothing signed and reports that something else holds the port.
2. **Requests.** Each request, WebSocket upgrades included, carries an HMAC-SHA256 keyed by the token over its method, request URI, time, a random nonce, the body hash and the epoch (`X-Portside-Date`, `X-Portside-Nonce`, `X-Portside-Epoch`, `X-Portside-Signature`).
3. **Replies.** Each request also carries a new challenge. The reply's proof covers the challenge, the epoch, the status and the SHA-256 of the body, so nothing between client and daemon can alter it; a WebSocket upgrade's proof covers the challenge and the epoch.

Each kind of HMAC has its own number of newline-joined parts, so one can never pass for another. The daemon accepts a signature within two minutes of its clock, each nonce once, and only for its current epoch; a client refused after a daemon restart repeats the handshake. A program that takes the daemon's port while the daemon is not running, for example under another account, therefore gets no signature, and one captured from an earlier daemon is refused by the next. Other accounts on the same machine cannot read the token and so cannot use the API.

For manual use, the daemon also accepts the token as `Authorization: Bearer <token>`, for example `curl -H "Authorization: Bearer $(cat ~/.local/state/portside/auth-token)" http://127.0.0.1:7878/api/state`. No shipped client sends it, and anything you send it to can reuse it.

## The dashboard host and session

The dashboard is served only at its own host, `http://d-<24 random hex digits>.localhost:<port>`. The host is new on every cold start of the daemon and written to `dashboard-host` (mode `0600`) beside the token. An in-place upgrade (`portside daemon restart`) keeps it, since the listening sockets are never released; an upgrade that has to listen anew gets a new host. No server may take a name of that form.

Browsers send cookies to every port of a host, so a cookie for `127.0.0.1` would reach every dev server, container or forwarded port opened there. The dashboard host is used by nothing else:

- **Sign-in.** `portside open` or the app creates a single-use link, valid for two minutes, on `http://127.0.0.1:<port>/auth`, so it does not name the dashboard host. The daemon forwards it to the dashboard host, and redeems it there, only on a connection from another process of its own account. On Linux it finds the connection's owner in `/proc/net/tcp` and `tcp6`; on macOS with `lsof`, which shows an account only its own processes. A connection it cannot attribute is refused, learns nothing of the dashboard host, and leaves the link unused. Other accounts can read process arguments, so a link they see there is of no use to them; `portside open` passes it to the browser directly and prints it only when no browser can take it ([CLI](cli.md#opening-the-dashboard)). The app loads the link in its own window.
- **Session cookie.** Redeeming the link sets an `HttpOnly`, `SameSite=Strict` cookie for the dashboard host. Its value is an HMAC of the token and the host, not the token. The daemon accepts it only on requests to the current dashboard host and only on connections from its own account (looked up once per connection), so a program of another account that obtained it cannot use it.
- **Old hosts.** A request for an earlier dashboard host gets the page on how to open the dashboard, and the dashboard's session probe hears it is signed out; neither points to the new host. A page another program served on the old host while the port was free, such as a service worker, has no hold on the dashboard, and old cookies stop working. Browser tabs from before a reboot or a daemon stop need `portside open` again; the app's window signs in again by itself.
- **Loopback names.** `127.0.0.1` and `localhost` serve the API to signed clients and, in a browser, the same page on how to open the dashboard. They never reveal the dashboard host, except by forwarding a valid sign-in link opened from your account.

## Browser requests

- A request whose `Host` is not the dashboard host, `127.0.0.1`, `localhost`, `[::1]`, `portside.localhost` or a `<name>.localhost` name of the proxy is rejected, which defeats DNS rebinding.
- State-changing requests and WebSockets that carry an `Origin` must come from a page on one of those daemon hosts, either the request's own origin or one on the daemon's port.
- Project icons are served only as images, with a sandboxing Content-Security-Policy, so an SVG in a project cannot run script on the dashboard's origin. Log replies are plain text with `X-Content-Type-Options: nosniff`.

## Servers reachable from the network

Your servers and containers are another matter. Many dev tools listen on every interface by default (Compose's `"5432:5432"`, `next dev`, Express's `app.listen(port)`, Go's `":8080"`, `vite --host`), and Portside keeps them running as you move between networks, so a database or dev server can be open to everyone on a café's Wi-Fi. The macOS firewall is off by default. Portside points these listeners out and changes nothing.

It reads, from the host's own sockets (`lsof`, or `ss` on Linux), the addresses each running server listens on, on its port and on any port of its process tree, and those of each container port a local engine publishes (an engine in a VM may forward a port published on `0.0.0.0` from `127.0.0.1` only). A listener counts as:

- `loopback` on `127.0.0.0/8`, `::1` and the loopback interface's link-local address only;
- `tailnet` on this machine's own Tailscale addresses only, plus loopback. They come from the daemon's cached `tailscale status`, read again at most once a minute and only while a listener uses an address in Tailscale's ranges. `100.64.0.0/10` is also carrier-grade NAT space that some networks assign to devices, so another address there, or any while Tailscale is unavailable, counts as `network`;
- `network` on all interfaces (`0.0.0.0`, `::`, `*`) or any other address.

The addresses show what a listener accepts, not what the firewall lets in. A `network` listener gets **May be reachable from the network** in the dashboard (on the server's card, row and header, and next to the container port) with the fix for its tool, a marker in `portside status`, a line in `portside doctor` and `networkWarning` in `list_servers`. The addresses are read when a server becomes ready and then every 30 seconds with a usage sample; container ports are read with each container resync.

To keep a listener on this computer, bind it to `127.0.0.1`:

| Tool | Setting |
| --- | --- |
| Compose | `"127.0.0.1:<host port>:<container port>"`, such as `"127.0.0.1:5432:5432"` |
| Vite, Astro, Nuxt, the Remix and React Router dev servers | no `--host`, or `--host 127.0.0.1` |
| `next dev`, Nuxt 2 | `--hostname 127.0.0.1` |
| `remix-serve`, `react-router-serve` | `HOST=127.0.0.1` |
| Node | `app.listen(port, '127.0.0.1')` |
| Go | `"127.0.0.1:8080"` |
| Rails | `bin/rails server -b 127.0.0.1` |
| Phoenix | `http: [ip: {127, 0, 0, 1}]` in `config/dev.exs` |
| Uvicorn, `fastapi`, `flask run` | `--host 127.0.0.1` |
| Django | `manage.py runserver 127.0.0.1:8000` |
| Gunicorn, Hypercorn | `--bind 127.0.0.1:8000` |
| `python3 -m http.server` | `--bind 127.0.0.1` |

The dashboard picks the fix from what the server's command runs, following package scripts; for other commands it gives a general hint. To open such a server from your other computer, connect the two ([Other computers](other-computers.md)), which forwards its port to loopback there. When a server is meant to be reachable, set `lan = true` in its table ([Configuration](configuration.md#server-keys)); that also silences the containers it owns. `exposure` stays in the API either way.

Limits:

- A listener that `lsof` cannot see, such as a rootful engine's `docker-proxy` on Linux, and the ports of an engine on another computer get no `exposure` and no mark; `portside doctor` lists such published ports of running containers in one informational line.
- Only TCP listeners are checked; UDP services get no mark.

## Killing processes

Killing a foreign process is limited to processes listening on a port. It refuses, by process name, container engines and their port forwarders (Docker, OrbStack, Colima, Podman and the others listed in [Containers](containers.md#ownership-and-ports)), `launchd`, `systemd`, the AirPlay receiver and `rapportd`.

## Sharing

Sharing is off by default. When it is on, Tailscale Serve forwards tailnet requests to a loopback listener that answers only under a random path known to the Serve configuration. A request is accepted only when all of these hold:

- it is addressed to this computer's tailnet name or address;
- it comes from another device of the user who owns this computer's node, not tagged (Tailscale sets `Tailscale-User-Login` and the source address itself);
- it does not come from this computer, so other accounts, apps and containers here cannot use the tailnet way in;
- on Linux, where every local account can read the Serve configuration and so the random path, its connection to the loopback listener comes from root or your own account, which is what `tailscaled` runs as; other local accounts cannot send it forged Tailscale headers (to guess pairing codes and get the current one withdrawn). On macOS there is no such check, since an account cannot see the sockets of Tailscale's root-owned network extension;
- it carries a header that web pages cannot send without a CORS preflight, which is never granted;
- it is signed with the key this daemon issued to that same device when it was paired, for the sharing listener's current epoch. Only the hello and pairing requests work without a signature.

Tailscale identifies devices, not accounts or processes, so the key is what keeps other accounts on your other devices out. It is never sent after pairing: each request carries an HMAC of its method, URI, time, a single-use nonce, body and epoch. Every connection goes over HTTPS, and the connecting daemon checks the certificate Tailscale holds for the shared computer's tailnet name. Every reply, WebSocket upgrades included, must also prove with the same key that it comes from the paired daemon, so after pairing a program posing as the shared daemon gets nothing reusable and its answers are refused. Keys are stored in `share-devices.json` on the shared computer and `remote-tokens.json` on the connecting one, both mode `0600` in the state directory. Unpairing a device, or pairing it again, ends the requests and connections of the old pairing at once, WebSockets and tunnels included; a request whose body was still arriving when the pairing ended is refused. The body of a signed request must arrive within 30 seconds, since it is read before the signature can be checked.

Pairing has no key yet to check: the code goes to whatever answers for that computer's tailnet name with its HTTPS certificate. A code is valid for 10 minutes, works once, is withdrawn after 10 wrong guesses, and is accepted only from another device of your Tailscale user. `portside pair` reads it from standard input, not from its arguments, which other accounts on the computer can read and could use to pair their own daemon first.

A paired device has the same control you have here, which includes running any command through a server definition. It can also open TCP tunnels to the ports of configured servers (to whatever listens there) and to the ports published by running containers. The daemon refuses it stopping or upgrading the daemon, opening desktop apps, coding-agent setup, sign-in links, connecting onward and changing sharing, but that guards against mistakes, not against a device that can edit the config. Pair only devices you trust as much as this one. You get a notification when a device pairs and when a paired device connects, and the **Computers** window shows which are connected now and when each last connected.

On the computer you connect from, each forwarded port listens on loopback like a server started there, and neither it nor the `<name>.<computer>.localhost` proxy checks which program connects. Other accounts on that computer and pages in its browsers reach the other computer's server through them just as they would reach a local one. A database without a password is as open as one you run locally; depending on the container engine, local containers may reach it too, through `host.docker.internal`. Turn off forwarding of ports you do not need there, or disconnect to close all of them. A computer that was taken over can still make this one listen on loopback ports, never on the daemon's own ports, and never on so many that the daemon runs out of file descriptors; a reply it relays to this computer is cut off at 64 MiB ([Forwarded ports](other-computers.md#forwarded-ports)).

The other direction is closed: replies the dashboard gets from the other computer (`/api/peer`) are served with a sandboxing Content-Security-Policy and without cookies, site data or redirect headers, so the computer you connect to cannot run script on this dashboard's origin, even when it was taken over. Its addresses always open on this computer (at the forwarded port or through the `<name>.<computer>.localhost` proxy), a state of an unexpected shape is ignored, a reply that stops for a minute is cut off, redirects are never followed, and the CLI and MCP tools show its names and notes as data: control characters are replaced in tables, and the MCP tools say the fields are not instructions.

## What it does not protect against

- Programs running as your own user can read the token, just as they could run the commands themselves.
- A page served on another port of the dashboard host (`http://d-....localhost:<other port>`) would receive the session cookie, so keep that host to yourself while it lasts. The daemon accepts the cookie only on your account's connections, so this matters only against your own programs, which can read the token anyway.
- The `*.localhost` proxy needs no token: it forwards to dev servers that already listen on loopback here, and while connected to servers on the other computer, which forwarded ports expose anyway.
