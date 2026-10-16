import { useSyncExternalStore } from 'react'
import { api, wsUrl } from '../api/client'
import type { State } from '../api/types'

export type Connection = 'connecting' | 'open' | 'reconnecting' | 'unauthorized'

interface Live {
  state: State | null
  connection: Connection
}

/** The daemon encodes empty lists as null. */
function normalize(s: State): State {
  const docker = s.docker ?? { status: 'connecting', engine: 'Docker', containers: [] }
  return { ...s, servers: s.servers ?? [], groups: s.groups ?? [], docker: { ...docker, containers: docker.containers ?? [] } }
}

let snapshot: Live = { state: null, connection: 'connecting' }
const listeners = new Set<() => void>()

function publish(next: Partial<Live>) {
  snapshot = { ...snapshot, ...next }
  listeners.forEach((l) => l())
}

let socket: WebSocket | null = null
let retry = 0
let timer: number | undefined
let started = false

async function open() {
  // Without a session the socket would be refused; say so instead of retrying blindly.
  try {
    const s = await api.session()
    if (!s.authenticated) {
      publish({ connection: 'unauthorized' })
      timer = window.setTimeout(() => void open(), 3000)
      return
    }
  } catch {
    /* daemon unreachable: the socket below fails and schedules a retry */
  }
  const ws = new WebSocket(wsUrl('/api/events'))
  socket = ws
  ws.onopen = () => {
    retry = 0
    publish({ connection: 'open' })
  }
  ws.onmessage = (ev) => {
    if (typeof ev.data !== 'string') return
    try {
      publish({ state: normalize(JSON.parse(ev.data) as State), connection: 'open' })
    } catch {
      /* ignore malformed frame */
    }
  }
  ws.onclose = () => {
    if (socket !== ws) return
    socket = null
    publish({ connection: 'reconnecting' })
    const delay = Math.min(10_000, 500 * 2 ** retry++)
    timer = window.setTimeout(() => void open(), delay)
  }
  ws.onerror = () => ws.close()
}

export function startLive() {
  if (started) return
  started = true
  void open()
  window.addEventListener('online', () => {
    if (!socket) {
      window.clearTimeout(timer)
      retry = 0
      void open()
    }
  })
}

/** Mutations that answer with a full State feed it straight into the store. */
export function applyState(state: State) {
  publish({ state: normalize(state) })
}

function subscribe(cb: () => void) {
  listeners.add(cb)
  return () => listeners.delete(cb)
}

export function useLive(): Live {
  return useSyncExternalStore(subscribe, () => snapshot)
}
