import type { ServerView } from '../../api/types'

export function formatBytes(n: number): string {
  if (n <= 0) return '0 MB'
  const mb = n / (1024 * 1024)
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)} GB`
  return `${mb >= 10 ? Math.round(mb) : mb.toFixed(1)} MB`
}

export const formatCpu = (p: number) => `${p >= 10 ? Math.round(p) : p.toFixed(1)}%`

export function formatUptime(since: string | undefined, now: number): string {
  if (!since) return ''
  const s = Math.max(0, Math.floor((now - Date.parse(since)) / 1000))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, '0')}s`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ${String(m % 60).padStart(2, '0')}m`
  return `${Math.floor(h / 24)}d ${h % 24}h`
}

/** `45 s`, `3 min`, `2 h`, `4 d`: how long ago, for stopped servers. Units read the same in English and Polish. */
export function formatAgo(since: string | undefined, now: number): string {
  if (!since) return ''
  const s = Math.max(0, Math.floor((now - Date.parse(since)) / 1000))
  if (s < 60) return `${s} s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} min`
  const h = Math.floor(m / 60)
  return h < 24 ? `${h} h` : `${Math.floor(h / 24)} d`
}

/** When a scheduled run is due or began: the time today, the weekday and time within the coming week, else the date and time. */
export function formatRunTime(at: string, now: number): string {
  const d = new Date(at)
  if (Number.isNaN(d.getTime())) return ''
  const time = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  const today = new Date(now)
  today.setHours(0, 0, 0, 0)
  const days = (d.getTime() - today.getTime()) / 86_400_000
  if (days >= 0 && days < 1) return time
  if (days >= 0 && days < 7) return `${d.toLocaleDateString(undefined, { weekday: 'short' })} ${time}`
  const year = d.getFullYear() === today.getFullYear() ? undefined : 'numeric'
  return `${d.toLocaleDateString(undefined, { day: 'numeric', month: 'short', year })} ${time}`
}

/** The time a chart covers, for example `2 min` or `45 s`. */
export function formatSpan(ms: number): string {
  const s = Math.round(ms / 1000)
  if (s < 90) return `${s} s`
  const m = ms / 60000
  return `${m >= 10 ? Math.round(m) : Math.round(m * 10) / 10} min`
}

export function shortenHome(p: string): string {
  return p.replace(/^\/(?:Users|home)\/[^/]+/, '~')
}

/** `http://name.localhost:7980/x` becomes `name.localhost:7980/x`. */
export const bareUrl = (u: string) => u.replace(/^https?:\/\//, '').replace(/\/$/, '')

/** A process runs and is not shutting down: its address opens and its terminal takes input. */
export const isUp = (v: Pick<ServerView, 'status'>) => v.status === 'running' || v.status === 'starting'

export const hasProcess = (v: Pick<ServerView, 'status'>) =>
  isUp(v) || v.status === 'stopping'

/**
 * A crashed, exited or blocked server waiting for its automatic restart (or
 * a retry after `retry_after`). Daemons
 * without the field say so in the note, which a stop leaves in place, so
 * there the server must also still be wanted.
 */
export const restartPending = (v: Pick<ServerView, 'status' | 'restartPending' | 'note' | 'wanted'>) =>
  (v.status === 'crashed' || v.status === 'exited' || v.status === 'blocked') && (v.restartPending ?? (v.wanted && /restarting in /.test(v.note ?? '')))

/** Stop has something to do: a process runs, or an automatic restart is due. */
export const canStop = (v: Pick<ServerView, 'status' | 'restartPending' | 'note' | 'wanted'>) =>
  isUp(v) || restartPending(v)

export function initials(name: string): string {
  const parts = name.split(/[-_.]+/).filter(Boolean)
  if (parts.length >= 2) return ((parts[0]?.[0] ?? '') + (parts[1]?.[0] ?? '')).toUpperCase()
  return name.slice(0, 2).toUpperCase()
}

/** Picks one of the six per-theme project colors (--project-1 to --project-6) from the name. */
export function projectColor(name: string): number {
  let h = 0
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) % 6
  return h + 1
}

export const NAME_RE = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/
