# Containers

Portside talks to the Docker Engine API of the active Docker context, so it works the same with Docker Desktop, OrbStack, Colima, Rancher Desktop (with dockerd), Podman or a Linux daemon, rootful or rootless, and with remote `tcp://` (also TLS) and `ssh://` contexts.

## Containers next to their server

Compose containers appear next to the server with the closest matching directory, including projects in a subfolder such as `infra/` and ones that project scripts start: state, health check, uptime, published ports, CPU and memory, updated live. Servers that share the closest directory share its containers, and a temporary worktree server owns the containers started from its worktree (also one nested in the original checkout). Supabase CLI containers match `project_id` in `supabase/config.toml` in the server directory or an ancestor up to the repository root. Set per-server `projects` to explicitly attach Compose project names.

A server warns when one of its containers is unhealthy, restarting or crashed while it runs; a container stopped on purpose (a stop signal from `docker stop`, `compose stop` or `docker kill`, remembered across daemon restarts, or an exit the engine did not restart under an `always` or `unless-stopped` policy without an error) does not count as crashed. Attached containers becoming unhealthy or failing trigger desktop notifications when notifications are enabled and no notifying desktop client is connected.

## Actions

Each container has start, stop and restart, its logs in a searchable terminal, a shell (bash or sh) in a terminal tab, details (image, command, environment with secret-looking values hidden until revealed, volumes, networks) and, for Compose services, **Recreate**, plus **Rebuild and recreate** for known Compose-built images or **Pull and recreate** for image services. Each Compose project offers **Start all**, **Stop all** and confirmed **Remove containers** (`compose down`, keeping volumes) on both container views. Log-driver errors stay visible above the terminal, and output survives reconnection.

A **Containers** page (`g d`) shows every container on the engine with the same actions. When the engine is down, **Start Docker** starts the one behind the active context, or the `docker_start` command. **Start Docker** on Linux starts rootless Docker's user unit (`systemctl --user start docker`) or Podman's socket; for Docker as a system service it tells you to run `sudo systemctl start docker` instead.

Recreate and rebuild run `docker compose up --detach --no-deps --force-recreate [--build] <service>` with the project's own files, because the engine API has no notion of Compose projects; without a `docker` command whose `docker compose version` works they use `docker-compose`, `podman compose` or `podman-compose`, in that order.

## Finding the engine

Containers come from the Docker Engine API of the active Docker context (`DOCKER_HOST`, `DOCKER_CONTEXT`, then `currentContext` in `~/.docker/config.json`), read from your login-shell environment. Without one, the first socket that exists is used: `/var/run/docker.sock`, then on macOS OrbStack, Docker Desktop, Colima, Rancher Desktop and Podman machine sockets, on Linux rootless Docker (`$XDG_RUNTIME_DIR/docker.sock`), Docker Desktop, rootless Podman (`$XDG_RUNTIME_DIR/podman/podman.sock`, from `systemctl --user enable --now podman.socket`) and `/run/podman/podman.sock`. Where `/var/run/docker.sock` is a symlink, its target names the engine.

A `tcp://` endpoint uses TLS when `DOCKER_TLS_VERIFY` or `DOCKER_TLS` is set (certificates from `DOCKER_CERT_PATH`, else `~/.docker`) or when its context has TLS material, as the docker CLI does. A `tcp://` host without a port uses 2375, or 2376 with TLS.

An `ssh://` endpoint runs `ssh <host> docker system dial-stdio` in batch mode, so the key must come from your SSH agent or config without a prompt; keepalives (every 15 seconds, three missed) end a dead link so the connection is made again, and containers on another machine are listed and managed, but their ports are not treated as local.

When the Rancher Desktop socket or context does not answer and its settings choose containerd, which has no Docker API, Portside says so and asks for dockerd (moby).

An event stream keeps the list current and a stats stream per running container feeds CPU and memory; over `ssh://`, stats are polled on an interval over a few shared connections instead, so sshd does not see a login per container.

## Ownership and ports

A container belongs to the server whose `dir` is its `com.docker.compose.project.working_dir`; when several servers share that directory, the one running `docker compose` wins, else the first.

A `docker compose` server whose containers are still up from an earlier run starts even though its ports are taken, since Compose attaches to them. The port counts as the stack's when its listener is a container engine process (Docker's proxy, OrbStack, Colima, Podman with gvproxy, pasta, slirp4netns or conmon, rootless Docker's rootlesskit or rootlessport) or, when the listener is hidden, as rootful Docker's root-owned `docker-proxy` is on Linux, when a running container of that server (by the ownership rules above) on a local engine publishes it; a publishing container that belongs to no server also counts, with a note in the daemon log, and one of another server's projects does not.
