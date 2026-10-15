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

/** `3m`, `2h`, `4d`: how long ago, for stopped servers. */
export function formatAgo(since: string | undefined, now: number): string {
  if (!since) return ''
  const s = Math.max(0, Math.floor((now - Date.parse(since)) / 1000))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  return h < 24 ? `${h}h` : `${Math.floor(h / 24)}d`
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

export const isAlive = (v: Pick<ServerView, 'status'>) => v.status === 'running' || v.status === 'starting'

export const hasProcess = (v: Pick<ServerView, 'status'>) =>
  v.status === 'running' || v.status === 'starting' || v.status === 'stopping'

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
