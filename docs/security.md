# Security model

The daemon runs arbitrary commands from your config, so its API must only be reachable by you. To report a vulnerability, see [SECURITY.md](../SECURITY.md).

## Trust

What Portside can do, and how to check what you install.

- **It acts as you.** The daemon runs the commands in your config with your account's permissions, in no sandbox. Treat a server definition like a script you run yourself.
- **Nothing leaves the machine on its own.** Portside sends no telemetry and checks for no updates. It listens only on loopback; it connects out only to the Docker engine of your active Docker context, and to your own computers over Tailscale once you turn on sharing or pair one.
- **Coding agents get the MCP tools, not a shell.** Through MCP an agent can list and read every project's servers, containers, logs and ports, start or reuse configured servers, restart and stop them, and start, stop, restart or recreate containers. It cannot run other commands, edit the config or remove volumes through MCP. `portside mcp --read-only` offers only the reading tools ([Coding agents](coding-agents.md#read-only-tools)). An agent that also has a shell can use the CLI anyway, so this limits the MCP tools, not the agent.
- **What it stores.** The config and the state directory (`~/.config/portside`, `~/.local/state/portside`) hold the token, server logs and pairing keys, readable only by your account. Server logs keep whatever your servers print, secrets included.
- **The macOS app is not notarized.** It is signed ad hoc, not with an Apple Developer ID, so Apple has not scanned it, and the Homebrew cask removes the quarantine flag so that Gatekeeper does not block the first launch. What you rely on instead is that the source is public and each release is built from a tagged commit by the [release workflow](../.github/workflows/release.yml) on GitHub Actions.
- **Checking a download.** From 0.3.0, every release file carries a build provenance attestation that ties it to that workflow run and commit: `gh attestation verify Portside-<version>.zip -R padmk/portside` (the same for the CLI archives and `checksums.txt`). Each CLI archive also has an SBOM. To avoid the prebuilt app altogether, build it from source, by hand or with the Homebrew formula that compiles the tagged source on your Mac (README, "Other ways to install").
- **Removing it.** `brew uninstall --zap --cask portside` stops the daemon and its servers and trashes the config and state; agent registrations are removed with the agents' own commands (`claude mcp remove --scope user portside`, `codex mcp remove portside`) and the `portside` skill folder in `~/.claude/skills` and `~/.agents/skills` (or `$CODEX_HOME/skills`).

## Loopback only

The daemon binds to loopback only, on both `127.0.0.1` and `[::1]` wherever the system has IPv6 loopback. Browsers try `::1` first for `*.localhost`, so the daemon refuses to start while another program holds `[::1]:<port>` (it would receive every dashboard request and its cookie), names the address in the error, and `portside doctor` checks that the daemon itself answers on both. Clients dial `127.0.0.1` by address.

## Signed requests

Every API call needs the token in `~/.local/state/portside/auth-token` (mode `0600`, created on first start), which is never sent. Each daemon start picks a random epoch.

Before its first signed request, the CLI, the MCP server and the macOS app ask `GET /api/health` with a random challenge (`X-Portside-Challenge`); the daemon answers with its epoch and a proof, an HMAC-SHA256 keyed by the token over the challenge and the epoch (`X-Portside-Epoch`, `X-Portside-Proof`). A client that gets no valid proof sends nothing signed and reports that something else holds the port.

Then each request, WebSocket upgrades included, is signed with an HMAC-SHA256 keyed by the token over its method, request URI, time, a random nonce, the body hash and the epoch (`X-Portside-Date`, `X-Portside-Nonce`, `X-Portside-Epoch`, `X-Portside-Signature`), and carries a new challenge whose proof the reply must hold. A reply's proof also covers its status and the SHA-256 of its body, so nothing between the client and the daemon can alter it; a WebSocket upgrade's covers the challenge and the epoch. Each kind of HMAC has its own number of newline-joined parts, so one can never pass for another.

The daemon accepts a signature within two minutes of its clock, each nonce once, and only for its current epoch; a client refused after a daemon restart repeats the handshake. A program that takes the daemon's port while the daemon is not running, for example under another account, therefore gets no signature, and one captured from an earlier daemon is refused by the next. Other accounts on the same machine cannot use the API.

For manual use, the daemon also accepts the token as `Authorization: Bearer <token>`, for example `curl -H "Authorization: Bearer $(cat ~/.local/state/portside/auth-token)" http://127.0.0.1:7878/api/state`. No shipped client sends it, and anything you send it to can reuse it.

## The dashboard host and session

The dashboard has its own host, `http://d-<24 random hex digits>.localhost:7878`, new on every cold start of the daemon and written to `dashboard-host` (mode `0600`) beside the token; an in-place upgrade (`portside daemon restart`) keeps it, since the listening sockets are never released (one that has to listen anew gets a new host). No server may take a name of that form.

A browser signs in through a single-use link, valid for two minutes, that `portside open` or the app creates and gets an `HttpOnly`, `SameSite=Strict` session cookie for that host only. Browsers send cookies to every port of a host, so a cookie for `127.0.0.1` would reach every dev server, container or forwarded port opened there; the dashboard host is used by nothing else. The cookie is an HMAC of the token and the host, not the token, and the daemon accepts it only on requests to the current dashboard host.

A new host each start means that a page another program served on the old one while the port was free, such as a service worker, has no hold on the dashboard, and old cookies stop working. A request for an old dashboard host gets the page on how to open the dashboard (and the dashboard's session probe hears it is signed out), never a pointer to the new host. So browser tabs from before a reboot or a daemon stop need `portside open` again; the app's window signs in again by itself. `127.0.0.1` and `localhost` serve the API to signed clients and, in a browser, the same page; they never reveal the dashboard host, except to forward a valid sign-in link opened from your account. A state directory from before host-bound sessions gets a new token on the first start, so cookies an older version set on `127.0.0.1` stop working.

The sign-in link is on `http://127.0.0.1:<port>/auth`, so it does not name the dashboard host, and the daemon forwards it to the dashboard host, and redeems it there, only on a connection from another process of its own account (on Linux it finds the connection's owner in `/proc/net/tcp` and `tcp6`; on macOS with `lsof`, which shows an account only its own processes); a connection it cannot attribute is refused, learns nothing of the dashboard host, and the link stays unused. A link that shows in process arguments, which other accounts can read, is therefore of no use to them, and `portside open` passes it to the browser command directly, printing it only when no browser can take it (see [CLI](cli.md#opening-the-dashboard)). The session cookie, too, is accepted only on connections from your account, so a program of another account that received it cannot use it. The owner is looked up once per connection, not per request. The app loads the link in its own window.

## Browser requests

- Requests whose `Host` is not the dashboard host, `127.0.0.1`, `localhost`, `[::1]` or `portside.localhost` are rejected, which defeats DNS rebinding.
- State-changing requests and WebSockets from browsers must come from the dashboard's own origin.
- Project icons are served only as images, with a sandboxing Content-Security-Policy, so an SVG in a project cannot run script on the dashboard's origin.

## Killing processes

Killing a foreign process is limited to processes currently listening on a port, and refuses container engines and their port forwarders (Docker, OrbStack, Colima, Podman and the others listed in [Containers](containers.md#ownership-and-ports)), launchd, systemd and the AirPlay receiver.

## Sharing

Sharing is off by default. When it is on, Tailscale Serve forwards tailnet requests to a loopback listener that answers only under a random path known to the Serve configuration. A request is accepted only when all of these hold:

- it comes from another device of the user who owns this computer's node, not tagged (Tailscale sets `Tailscale-User-Login` and the source address itself);
- it does not come from this computer, so other accounts, apps and containers here cannot use the tailnet way in;
- it carries a header that web pages cannot send without a CORS preflight, which is never granted;
- it is signed with the key this daemon issued to that same device when it was paired with a one-time code.

Tailscale identifies devices, not accounts or processes, so the key is what keeps other accounts on your other devices out. It is never sent after pairing: each request carries an HMAC of its method, URI, time, a single-use nonce and body. Every connection goes over HTTPS with the certificate Tailscale holds for the shared computer's tailnet name, and every reply, WebSocket upgrades included, must also prove with the same key that it comes from the paired daemon, so a program posing as the shared daemon gets neither the code nor anything reusable, and its answers are refused. Keys are stored in `share-devices.json` on the shared computer and `remote-tokens.json` on the connecting one, both mode `0600` in the state directory.

A paired device has the same control you have here, which includes running any command through a server definition; the dashboard does not offer it stopping the daemon, opening desktop apps or changing sharing, but that is a convenience, not a security boundary. Pair only devices you trust as much as this one.

## What it does not protect against

Programs running as your own user can read the token, just as they could run the commands themselves. Keep the dashboard host to yourself while it lasts: a page served on another port of that host (`http://d-....localhost:<other port>`) would receive the session cookie. The daemon accepts it only on your account's connections, so this matters only against your own programs, which can read the token anyway. The `*.localhost` proxy needs no token: it only forwards to dev servers that already listen on loopback.
