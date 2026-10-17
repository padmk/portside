import { useSyncExternalStore } from 'react'
import { api, isCurrent, setMachine, wsUrl } from '../api/client'
import type { RemoteConnection, ServerView, State } from '../api/types'
import { t } from '../i18n'
import { toast } from './toasts'

export type Connection = 'connecting' | 'open' | 'reconnecting' | 'unauthorized'

interface Live {
  /** The state of the computer shown: this one, or the remote while connected. */
  state: State | null
  connection: Connection
  /** The daemon-wide connection to another computer; every tab follows it. */
  remote: RemoteConnection | null
}

/** The remote's port is forwarded to the same port here, so localhost:<port> opens it. */
export const isMirrored = (r: RemoteConnection, port: number) => r.mirrors.some((m) => m.port === port && !m.local && m.state === 'mirrored')

/** The local port the remote's port is forwarded to, or 0 when it is not forwarded. */
const forwardedTo = (r: RemoteConnection, port: number) => {
  const m = r.mirrors.find((x) => x.port === port && x.state === 'mirrored')
  return m ? (m.local ?? m.port) : 0
}

/**
 * Server URLs as this computer can open them. A forwarded remote port keeps
 * its direct URL, at the local port it is forwarded to; anything else goes
 * through the local daemon's proxy at <server>.<peer>.localhost.
 */
function localUrls(v: ServerView, r: RemoteConnection): ServerView {
  const proxied = (raw: string) => {
    try {
      const u = new URL(raw)
      return `http://${v.name}.${r.peer.label}.localhost:${r.localPort}${u.pathname}${u.search}`
    } catch {
      return raw
    }
  }
  let url = v.url
  if (url) {
    try {
      const u = new URL(url)
      const port = Number(u.port || (u.protocol === 'https:' ? 443 : 80))
      const local = forwardedTo(r, port)
      if (!local) url = proxied(url)
      else if (local !== port) {
        u.port = String(local)
        url = u.toString()
      }
    } catch {
      /* keep the original */
    }
  }
  return { ...v, url, alias: v.alias ? proxied(v.alias) : v.alias }
}

/** The daemon encodes empty lists as null. */
function normalize(s: State, remote: RemoteConnection | null): State {
  const docker = s.docker ?? { status: 'connecting', engine: 'Docker', containers: [] }
  const servers = s.servers ?? []
  return {
    ...s,
    servers: remote ? servers.map((v) => localUrls(v, remote)) : servers,
    groups: s.groups ?? [],
    docker: { ...docker, containers: docker.containers ?? [] },
    remote,
  }
}

const normalizeRemote = (r: RemoteConnection): RemoteConnection => ({ ...r, mirrors: r.mirrors ?? [] })

let snapshot: Live = { state: null, connection: 'connecting', remote: null }
const listeners = new Set<() => void>()

function publish(next: Partial<Live>) {
  snapshot = { ...snapshot, ...next }
  listeners.forEach((l) => l())
}

/** The last state as the daemon sent it, before URLs are rewritten for the remote. */
let raw: State | null = null

let socket: WebSocket | null = null
let retry = 0
let timer: number | undefined
let started = false

const backoff = () => Math.min(10_000, 500 * 2 ** retry++)

function enterRemote(next: RemoteConnection) {
  const remote = normalizeRemote(next)
  // Another computer's state never stands in for this one's, or for a different remote's.
  if (snapshot.remote?.peer.dnsName !== remote.peer.dnsName) raw = null
  setMachine(remote.peer.dnsName)
  publish({ remote, state: raw ? normalize(raw, remote) : null })
}

function leaveRemote() {
  const was = snapshot.remote
  if (!was) return
  setMachine(null)
  raw = null
  publish({ remote: null, state: null })
  toast(t('remote.disconnected', { name: was.peer.name }))
}

function replace(ws: WebSocket | null) {
  window.clearTimeout(timer)
  const old = socket
  socket = ws
  old?.close()
}

async function openLocal() {
  replace(null)
  // Without a session the socket would be refused; say so instead of retrying blindly.
  try {
    const s = await api.session()
    if (!s.authenticated) {
      publish({ connection: 'unauthorized' })
      timer = window.setTimeout(() => void openLocal(), 3000)
      return
    }
  } catch {
    /* daemon unreachable: the socket below fails and schedules a retry */
  }
  if (socket) return
  const ws = new WebSocket(wsUrl('/api/events'))
  socket = ws
  ws.onopen = () => {
    // During a hand-off from the remote stream the peer stream's own retries count.
    if (snapshot.remote) return
    retry = 0
    publish({ connection: 'open' })
  }
  ws.onmessage = (ev) => {
    if (socket !== ws || typeof ev.data !== 'string') return
    let msg: State
    try {
      msg = JSON.parse(ev.data) as State
    } catch {
      return
    }
    if (msg.remote) {
      enterRemote(msg.remote)
      openPeer()
      return
    }
    leaveRemote()
    // A hand-off from the remote stream ends here; this stream's own retries start over.
    retry = 0
    raw = msg
    publish({ state: normalize(msg, null), connection: 'open' })
  }
  ws.onclose = () => {
    if (socket !== ws) return
    socket = null
    publish({ connection: 'reconnecting' })
    timer = window.setTimeout(() => void openLocal(), backoff())
  }
  ws.onerror = () => ws.close()
}

/** The remote's live state through the local daemon. */
function openPeer() {
  const ws = new WebSocket(wsUrl(api.eventsPath()))
  replace(ws)
  ws.onmessage = (ev) => {
    if (socket !== ws || typeof ev.data !== 'string') return
    let msg: Partial<State> & { remote?: RemoteConnection | null }
    try {
      msg = JSON.parse(ev.data) as typeof msg
    } catch {
      return
    }
    retry = 0
    if (!msg.remote) {
      // The connection ended; the local stream takes over and reports it.
      void openLocal()
      return
    }
    if (snapshot.remote?.peer.dnsName !== msg.remote.peer.dnsName) enterRemote(msg.remote)
    const remote = normalizeRemote(msg.remote)
    // Until the remote answers, a frame carries only the connection.
    if (msg.daemon && msg.servers !== undefined) raw = msg as State
    publish({ remote, state: raw ? normalize(raw, remote) : null, connection: 'open' })
  }
  ws.onclose = () => {
    if (socket !== ws) return
    socket = null
    // The daemon closes this socket when the connection ends; the local stream says whether it did.
    const delay = retry === 0 ? 0 : backoff()
    retry++
    timer = window.setTimeout(() => void openLocal(), delay)
  }
  ws.onerror = () => ws.close()
}

export function startLive() {
  if (started) return
  started = true
  void openLocal()
  window.addEventListener('online', () => {
    if (!socket) {
      retry = 0
      void openLocal()
    }
  })
}

/** Switches this tab to a connection made here, without waiting for the daemon's broadcast. */
export function followRemote(remote: RemoteConnection) {
  if (snapshot.remote?.peer.dnsName === remote.peer.dnsName) return
  retry = 0
  enterRemote(remote)
  openPeer()
}

/** Returns to this computer's stream after a disconnect made here. */
export function followLocal() {
  retry = 0
  void openLocal()
}

/**
 * Mutations that answer with a full State feed it straight into the store,
 * unless the dashboard has switched computers since the request was made.
 */
export function applyState(state: State) {
  if (!isCurrent(state)) return
  raw = state
  publish({ state: normalize(state, snapshot.remote) })
}

/** The connected remote, for code outside React render. */
export const currentRemote = () => snapshot.remote

function subscribe(cb: () => void) {
  listeners.add(cb)
  return () => listeners.delete(cb)
}

export function useLive(): Live {
  return useSyncExternalStore(subscribe, () => snapshot)
}

/** The connected remote, or null on this computer. */
export const useRemote = () => useLive().remote
