import type {
  BulkAction,
  BulkResult,
  ContainerDetails,
  DiscoverResponse,
  ListenerView,
  OpenTarget,
  PairingCode,
  PeersResponse,
  RemoteConnection,
  RemoteOverview,
  ServerView,
  Spec,
  State,
} from './types'

export class ApiError extends Error {
  status: number
  code?: string
  constructor(message: string, status: number, code?: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

/**
 * A response to a request made for a computer the dashboard no longer shows.
 * Callers drop it silently: its data and its errors belong to that computer.
 */
export class StaleResponseError extends Error {
  constructor() {
    super('response from a computer no longer shown')
  }
}

export const isStale = (e: unknown) => e instanceof StaleResponseError

/** The message to show for a failed request, or null when it should not be shown. */
export const errorText = (e: unknown): string | null => (isStale(e) ? null : e instanceof Error ? e.message : String(e))

/**
 * Changes whenever the computer the dashboard shows changes. A request captures
 * it when dispatched; a response from an older generation is never published.
 */
let generation = 0
const stamps = new WeakMap<object, number>()

/** False for a response to a request made for a computer no longer shown. */
export const isCurrent = (data: object) => (stamps.get(data) ?? generation) === generation

async function request<T>(method: string, path: string, body?: unknown, scoped = false): Promise<T> {
  const gen = generation
  const stale = () => scoped && gen !== generation
  let res: Response
  try {
    res = await fetch(path, {
      method,
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    if (stale()) throw new StaleResponseError()
    throw new ApiError('daemon unreachable', 0)
  }
  const text = await res.text()
  if (stale()) throw new StaleResponseError()
  let data: unknown = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = null
    }
  }
  if (!res.ok) {
    const e = (data ?? {}) as { error?: string; code?: string }
    throw new ApiError(e.error || res.statusText || `request failed (${res.status})`, res.status, e.code)
  }
  if (scoped && data !== null && typeof data === 'object') stamps.set(data, gen)
  return data as T
}

/**
 * Prefix of every daemon route that acts on the computer shown: '/api' for this
 * computer, `/api/peer/<dns name>` while connected to another one; the daemon
 * refuses a peer request naming a computer it is no longer connected to, so a
 * request never runs on a different computer than the one shown. Session and remote
 * routes always address the local daemon.
 */
const LOCAL = '/api'
const PEER_BASE = '/api/peer'
let base = LOCAL
let machine: string | null = null

/** Points the API at this computer (null) or at the connected peer, by its DNS name. */
export function setMachine(peer: string | null) {
  if (peer === machine) return
  machine = peer
  base = peer === null ? LOCAL : `${PEER_BASE}/${encodeURIComponent(peer)}`
  generation++
}

/** A request to the computer shown; its response is dropped once another computer is shown. */
const on = <T>(method: string, path: string, body?: unknown) => request<T>(method, `${base}${path}`, body, true)

const enc = encodeURIComponent
/** The daemon encodes empty lists as null. */
const overview = (r: RemoteOverview): RemoteOverview => ({ ...r, share: { ...r.share, devices: r.share.devices ?? [] } })
const server = (name: string) => `/servers/${enc(name)}`
const container = (name: string) => `/containers/${enc(name)}`

export const api = {
  state: () => on<State>('GET', '/state'),
  eventsPath: () => `${base}/events`,

  start: (name: string, takeover = false) => on<ServerView>('POST', `${server(name)}/start`, { takeover }),
  stop: (name: string) => on<ServerView>('POST', `${server(name)}/stop`, {}),
  restart: (name: string, takeover = false) => on<ServerView>('POST', `${server(name)}/restart`, { takeover }),
  bulk: (action: BulkAction, target: { names: string[] } | { all: true }) =>
    on<{ results: BulkResult[] }>('POST', '/bulk', { action, ...target }),
  clearLogs: (name: string) => on<{ ok: boolean }>('DELETE', `${server(name)}/logs`),
  iconUrl: (name: string) => `${base}${server(name)}/icon`,
  terminalPath: (name: string) => `${base}${server(name)}/terminal`,

  spec: (name: string) => on<Spec>('GET', `${server(name)}/spec`),
  createServer: (spec: Spec) => on<State>('POST', '/servers', spec),
  updateServer: (name: string, spec: Spec) => on<State>('PUT', server(name), spec),
  deleteServer: (name: string) => on<State>('DELETE', server(name)),
  setEnabled: (name: string, enabled: boolean) => on<State>('POST', `${server(name)}/enabled`, { enabled }),

  config: () => on<{ path: string; text: string; revision: string }>('GET', '/config'),
  saveConfig: (text: string, revision: string) => on<State>('PUT', '/config', { text, revision }),
  session: () => request<{ authenticated: boolean }>('GET', `${LOCAL}/session`),

  listeners: () => on<{ listeners: ListenerView[] }>('GET', '/listeners'),
  killListener: (pid: number, port: number) => on<{ ok: boolean }>('POST', `/listeners/kill`, { pid, port }),
  discover: (root?: string) =>
    on<DiscoverResponse>('GET', `/discover${root ? `?root=${enc(root)}` : ''}`).then((r) => ({
      ...r,
      suggestions: r.suggestions ?? [],
    })),
  freePort: () => on<{ port: number }>('GET', '/free-port'),

  container: (name: string) => on<ContainerDetails>('GET', container(name)),
  containerAction: (name: string, action: 'start' | 'stop' | 'restart') => on<{ ok: boolean }>('POST', `${container(name)}/${action}`, {}),
  recreate: (name: string, build: boolean) => on<{ ok: boolean }>('POST', `${container(name)}/recreate`, { build }),
  dismissJob: (name: string) => on<{ ok: boolean }>('DELETE', `${container(name)}/job`),
  startEngine: () => on<{ ok: boolean }>('POST', `/docker/start`, {}),
  containerLogsPath: (name: string) => `${base}${container(name)}/logs`,
  containerShellPath: (name: string) => `${base}${container(name)}/shell`,
  containerJobPath: (name: string) => `${base}${container(name)}/job/terminal?always=1`,

  /** Desktop actions; a remote daemon refuses them. */
  open: (target: OpenTarget, name?: string) => on<{ ok: boolean }>('POST', '/open', { target, name }),
  shutdown: () => on<{ ok: boolean }>('POST', '/shutdown', {}),

  remote: () => request<RemoteOverview>('GET', `${LOCAL}/remote`).then(overview),
  peers: () => request<PeersResponse>('GET', `${LOCAL}/remote/peers`).then((r) => ({ ...r, peers: r.peers ?? [] })),
  /** Connects to a paired peer by DNS name; 401 with code `pairing_required` asks for a pairing code. */
  connect: (peer: string) => request<RemoteConnection>('POST', `${LOCAL}/remote/connect`, { peer }),
  /** Pairs with a peer using the code it issued, then connects; 403 `pairing_failed` for a wrong or expired code. */
  pair: (peer: string, code: string) => request<RemoteConnection>('POST', `${LOCAL}/remote/pair`, { peer, code }),
  disconnect: () => request<{ ok: boolean }>('POST', `${LOCAL}/remote/disconnect`, {}),
  /** Forwards the connected computer's port to another local port; 0 forwards it to the same number again. */
  forwardPort: (port: number, local: number) => request<RemoteConnection>('PUT', `${LOCAL}/remote/ports`, { port, local }),
  share: (enabled: boolean) => request<RemoteOverview>('PUT', `${LOCAL}/remote/share`, { enabled }).then(overview),
  pairingCode: () => request<PairingCode>('POST', `${LOCAL}/remote/share/code`, {}),
  revokeDevice: (id: string) => request<RemoteOverview>('DELETE', `${LOCAL}/remote/share/devices/${enc(id)}`).then(overview),
}

export function wsUrl(path: string): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}${path}`
}
