# Containers

Portside talks to the Docker Engine API of the active Docker context, so it works the same with Docker Desktop, OrbStack, Colima, Rancher Desktop (with dockerd), Podman or a Linux daemon, rootful or rootless, and with remote `tcp://` (also TLS) and `ssh://` contexts.

## Containers next to their server

Containers appear next to the server they belong to (see [Ownership and ports](#ownership-and-ports)) with their state, health check, uptime, published ports, CPU and memory, updated live. This covers Compose projects in a subfolder such as `infra/` and ones that project scripts start.

A server warns when one of its containers is unhealthy or restarting, or when one fails while the server runs. A failure is an exit with an error, an out-of-memory kill or a dead container. These do not count as failures:

- a stop on purpose: `docker stop`, `compose stop` or `docker kill` with a stop signal, remembered across daemon restarts;
- an exit after SIGINT or SIGTERM (codes 130 and 143);
- a container left exited without an engine error under an `always` or `unless-stopped` restart policy, which the engine would otherwise have restarted.

When notifications are enabled and no notifying desktop client is connected, a container of a server turning unhealthy or failing sends a desktop notification.

## Actions

Each container has start, stop and restart, its logs in a searchable terminal, a shell (bash, else sh) in a terminal tab, and details: image, command, environment (secret-looking values hidden until revealed), volumes and networks. Log-driver errors stay visible above the terminal, and output survives reconnection.

Compose services also have **Recreate**, plus **Rebuild and recreate** when the image is known to be built by Compose, or **Pull and recreate** for an image service. They run `docker compose up --detach --no-deps --force-recreate <service>` with the project's own files, adding `--build` or `--pull always`, because the Engine API has no notion of Compose projects.

Each Compose project offers **Start all**, **Stop all** and a confirmed **Remove containers** (`compose down`, keeping volumes) on both container views; these cover every profile of the project. While a Compose job runs, another one for the same service, or for the whole project, is refused.

Compose jobs use `docker compose`; without a `docker` command whose `docker compose version` works they use `docker-compose`, `podman compose` or `podman-compose`, in that order.

A **Containers** page (`g d`) shows every container on the engine with the same actions.

When the engine is down, **Start Docker** runs the config's [`docker_start`](configuration.md#editor-terminal-shell-and-docker) command when set. Otherwise it starts the engine behind the active context, or the first one installed of OrbStack, Docker Desktop, Colima, Rancher Desktop and Podman. On Linux it starts rootless Docker's user unit (`systemctl --user start docker`) or Podman's socket; for Docker as a system service it tells you to run `sudo systemctl start docker` instead. An engine on another computer has to be started there.

## Finding the engine

The engine is the one of the active Docker context, read from your login-shell environment: `DOCKER_HOST`, then `DOCKER_CONTEXT`, then `currentContext` in `config.json` of `$DOCKER_CONFIG` (default `~/.docker`). Without one, the first socket that exists is used:

1. `/var/run/docker.sock`; where it is a symlink, its target names the engine.
2. On macOS: OrbStack, Docker Desktop, Colima, Rancher Desktop and Podman machine sockets.
3. On Linux: rootless Docker (`$XDG_RUNTIME_DIR/docker.sock`), Docker Desktop, rootless Podman (`$XDG_RUNTIME_DIR/podman/podman.sock`, from `systemctl --user enable --now podman.socket`), then `/run/podman/podman.sock`.

A `tcp://` endpoint uses TLS when `DOCKER_TLS_VERIFY` or `DOCKER_TLS` is set (certificates from `DOCKER_CERT_PATH`, else the Docker config directory) or when its context has TLS material, as the docker CLI does. A `tcp://` host without a port uses 2375, or 2376 with TLS.

An `ssh://` endpoint runs `ssh <host> docker system dial-stdio` in batch mode, so the key must come from your SSH agent or config without a prompt. Keepalives (every 15 seconds, three missed) end a dead link so the connection is made again.

Containers of an engine on another computer (`ssh://`, or `tcp://` to a host other than loopback) are listed and managed, but their ports are not treated as local.

When the Rancher Desktop socket or context does not answer and its settings choose containerd, which has no Docker API, Portside says so and asks for dockerd (moby).

An event stream keeps the list current, and a stats stream per running container feeds CPU and memory. Over `ssh://`, stats are polled on an interval over a few shared connections instead, so sshd does not see a login per container.

## Ownership and ports

A container belongs to a server by the first rule that matches:

1. Its Compose project is in the server's [`projects`](configuration.md#server-keys).
2. Its Compose working directory (`com.docker.compose.project.working_dir`) is the server's directory or lies under it; the closest such directory wins.
3. Its Supabase CLI project matches `project_id` in `supabase/config.toml` in the server's directory or an ancestor, up to the repository root.

Servers matching equally share the container. In rule 2 that is every server with the same directory, led by the one running `docker compose`, then config order. In rules 1 and 3, the servers whose directory holds the container's working directory come first, the deepest first, so a temporary [worktree server](coding-agents.md#worktrees) owns the containers started from its worktree, also one nested in the original checkout.

A `docker compose` server whose containers are still up from an earlier run starts even though its ports are taken, since Compose attaches to them. The port counts as the stack's in these cases:

- Its listener is a container engine process: Docker's proxy or Docker Desktop, OrbStack, Colima or Lima, Podman with gvproxy, pasta, slirp4netns or conmon, or rootless Docker's rootlesskit or rootlessport.
- The listener is hidden, as rootful Docker's root-owned `docker-proxy` is on Linux, and a running container on a local engine publishes the port and belongs to that server: its Compose working directory is the server's directory, its project is in the server's `projects`, or the rules above give it to the server. A publishing container that belongs to no server also counts, with a note in the daemon log; one of another server's projects does not.
