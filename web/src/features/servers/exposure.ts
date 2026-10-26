import type { Key } from '../../i18n/dictionary'
import type { ContainerView } from '../../api/types'

/** How to bind a listener to loopback: an explanation and the code to use. */
export interface FixHint {
  text: Key
  code?: string
}

/**
 * The fix for each tool, by the server's `kind` (named by the daemon from
 * the command it runs). `port` is the server's port, 0 when it has none.
 */
const FIXES: Record<string, (port: number) => FixHint> = {
  vite: () => ({ text: 'network.fix.vite', code: '--host 127.0.0.1' }),
  remix: () => ({ text: 'network.fix.vite', code: '--host 127.0.0.1' }),
  'react-router': () => ({ text: 'network.fix.vite', code: '--host 127.0.0.1' }),
  astro: () => ({ text: 'network.fix.start', code: '--host 127.0.0.1' }),
  nuxt: () => ({ text: 'network.fix.start', code: '--host 127.0.0.1' }),
  nuxt2: () => ({ text: 'network.fix.start', code: '--hostname 127.0.0.1' }),
  next: () => ({ text: 'network.fix.start', code: '--hostname 127.0.0.1' }),
  'remix-serve': () => ({ text: 'network.fix.env', code: 'HOST=127.0.0.1' }),
  node: () => ({ text: 'network.fix.node', code: "app.listen(port, '127.0.0.1')" }),
  go: (port) => ({ text: 'network.fix.go', code: `"127.0.0.1:${port || 8080}"` }),
  rails: () => ({ text: 'network.fix.start', code: '-b 127.0.0.1' }),
  phoenix: () => ({ text: 'network.fix.phoenix', code: 'http: [ip: {127, 0, 0, 1}]' }),
  uvicorn: () => ({ text: 'network.fix.start', code: '--host 127.0.0.1' }),
  fastapi: () => ({ text: 'network.fix.start', code: '--host 127.0.0.1' }),
  flask: () => ({ text: 'network.fix.start', code: 'flask run --host 127.0.0.1' }),
  django: (port) => ({ text: 'network.fix.start', code: `manage.py runserver 127.0.0.1:${port || 8000}` }),
  gunicorn: (port) => ({ text: 'network.fix.start', code: `--bind 127.0.0.1:${port || 8000}` }),
  hypercorn: (port) => ({ text: 'network.fix.start', code: `--bind 127.0.0.1:${port || 8000}` }),
  'http.server': () => ({ text: 'network.fix.start', code: '--bind 127.0.0.1' }),
}

/**
 * The fix for a server, by its kind. A Compose server's comes from the
 * container port behind its port, or the first one reachable from the
 * network, among its containers.
 */
export function serverFix(kind: string | undefined, port: number, containers: ContainerView[]): FixHint {
  if (kind === 'compose') {
    const all = containers.flatMap((c) => c.ports ?? []).filter((p) => p.proto === 'tcp')
    const p = all.find((p) => p.networkWarning && p.public === port) ?? all.find((p) => p.networkWarning) ?? all.find((p) => p.public === port)
    return p ? composeFix(p.public, p.private) : composeFix(port, 0)
  }
  const fix = kind && Object.hasOwn(FIXES, kind) ? FIXES[kind] : undefined
  return fix ? fix(port) : { text: 'network.fix.other' }
}

/** The fix for a published container port: publish it on loopback only, keeping the container's own port. */
export function composeFix(published: number, container: number): FixHint {
  const host = published > 0 ? String(published) : '<host port>'
  return { text: 'network.fix.compose', code: `"127.0.0.1:${host}:${container > 0 ? container : '<container port>'}"` }
}
