import type {
  BulkAction,
  BulkResult,
  DiscoverResponse,
  ListenerView,
  OpenTarget,
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

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response
  try {
    res = await fetch(path, {
      method,
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    throw new ApiError('daemon unreachable', 0)
  }
  const text = await res.text()
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
  return data as T
}

const enc = encodeURIComponent
const server = (name: string) => `/api/servers/${enc(name)}`

export const api = {
  state: () => request<State>('GET', '/api/state'),

  start: (name: string, takeover = false) => request<ServerView>('POST', `${server(name)}/start`, { takeover }),
  stop: (name: string) => request<ServerView>('POST', `${server(name)}/stop`, {}),
  restart: (name: string, takeover = false) => request<ServerView>('POST', `${server(name)}/restart`, { takeover }),
  bulk: (action: BulkAction, target: { names: string[] } | { all: true }) =>
    request<{ results: BulkResult[] }>('POST', '/api/bulk', { action, ...target }),
  clearLogs: (name: string) => request<{ ok: boolean }>('DELETE', `${server(name)}/logs`),
  iconUrl: (name: string) => `${server(name)}/icon`,

  spec: (name: string) => request<Spec>('GET', `${server(name)}/spec`),
  createServer: (spec: Spec) => request<State>('POST', '/api/servers', spec),
  updateServer: (name: string, spec: Spec) => request<State>('PUT', server(name), spec),
  deleteServer: (name: string) => request<State>('DELETE', server(name)),
  setEnabled: (name: string, enabled: boolean) => request<State>('POST', `${server(name)}/enabled`, { enabled }),

  config: () => request<{ path: string; text: string; revision: string }>('GET', '/api/config'),
  saveConfig: (text: string, revision: string) => request<State>('PUT', '/api/config', { text, revision }),
  session: () => request<{ authenticated: boolean }>('GET', '/api/session'),

  listeners: () => request<{ listeners: ListenerView[] }>('GET', '/api/listeners'),
  killListener: (pid: number, port: number) => request<{ ok: boolean }>('POST', '/api/listeners/kill', { pid, port }),
  discover: (root?: string) =>
    request<DiscoverResponse>('GET', `/api/discover${root ? `?root=${enc(root)}` : ''}`).then((r) => ({
      ...r,
      suggestions: r.suggestions ?? [],
    })),
  freePort: () => request<{ port: number }>('GET', '/api/free-port'),

  open: (target: OpenTarget, name?: string) => request<{ ok: boolean }>('POST', '/api/open', { target, name }),
  shutdown: () => request<{ ok: boolean }>('POST', '/api/shutdown', {}),
}

export function wsUrl(path: string): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}${path}`
}
