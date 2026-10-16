import type { ContainerView, DockerState, ServerView } from '../../api/types'
import type { Key } from '../../i18n'
import type { DisplayStatus } from '../../ui/Lamp'

/** An exited container that failed rather than finished or was stopped; the daemon decides. */
export const failed = (c: ContainerView) => !!c.failed

/** The lamp and label a container is shown with. Health refines a running container. */
export function containerStatus(c: ContainerView): { lamp: DisplayStatus; label: Key } {
  switch (c.state) {
    case 'running':
      if (c.health === 'unhealthy') return { lamp: 'crashed', label: 'container.unhealthy' }
      if (c.health === 'starting') return { lamp: 'starting', label: 'container.healthStarting' }
      return { lamp: 'running', label: 'status.running' }
    case 'restarting':
      return { lamp: 'starting', label: 'container.restarting' }
    case 'removing':
      return { lamp: 'stopping', label: 'container.removing' }
    case 'paused':
      return { lamp: 'stopped', label: 'container.paused' }
    case 'created':
      return { lamp: 'stopped', label: 'container.created' }
    case 'dead':
      return { lamp: 'crashed', label: 'container.dead' }
    default:
      return failed(c) ? { lamp: 'crashed', label: 'container.failed' } : { lamp: 'exited', label: 'container.exited' }
  }
}

export const isRunning = (c: ContainerView) => c.state === 'running' || c.state === 'restarting' || c.state === 'paused'

/** The containers that belong to a server. */
export const containersOf = (docker: DockerState | undefined, server: string) =>
  docker?.containers.filter((c) => c.server === server) ?? []

/**
 * Containers that need attention: unhealthy or restarting ones, and while the
 * server runs, ones that failed since it started.
 */
export function containerIssues(v: ServerView, containers: ContainerView[]): ContainerView[] {
  const alive = v.status === 'running' || v.status === 'starting'
  const since = v.startedAt ? Date.parse(v.startedAt) : 0
  return containers.filter((c) => {
    if (c.state === 'running') return c.health === 'unhealthy'
    if (c.state === 'restarting') return true
    if (!alive || !failed(c)) return false
    return !c.finishedAt || Date.parse(c.finishedAt) >= since
  })
}

/** A server whose command runs containers, so the engine being down matters to it. */
export const usesDocker = (v: ServerView) => /\b(docker|docker-compose|podman)\b/.test(v.cmd)

/** The short name a container goes by: its compose service, else its name. */
export const shortName = (c: ContainerView) => c.service || c.name

/** Variable names whose values are hidden until revealed. */
const SECRET = /(pass(word)?|passwd|secret|token|api[_-]?key|private|credential|auth|cookie|session|signature|salt|dsn|access[_-]?key|(^|_)key$)/i

/** URLs that carry a password, such as postgres://user:pass@host/db. */
const URL_PASSWORD = /^[a-z][a-z0-9+.-]*:\/\/[^/@:\s]+:[^/@\s]+@/i

export const isSecret = (key: string, value: string) => SECRET.test(key) || URL_PASSWORD.test(value)
