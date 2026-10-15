import { useT } from '../i18n'
import type { ServerView } from '../api/types'

/** The status a server is displayed with; `external` is a stopped server whose port is held by another process. */
export type DisplayStatus = 'running' | 'starting' | 'stopping' | 'crashed' | 'blocked' | 'stopped' | 'exited' | 'external'

export function displayStatus(v: Pick<ServerView, 'status' | 'external'>): DisplayStatus {
  if (v.status === 'stopped' && v.external) return 'external'
  return v.status
}

export function Lamp({ status, className = '', decorative, large }: { status: DisplayStatus; className?: string; decorative?: boolean; large?: boolean }) {
  const t = useT()
  return (
    <span
      className={`lamp lamp--${status}${large ? ' lamp--lg' : ''} ${className}`}
      role={decorative ? undefined : 'img'}
      aria-hidden={decorative || undefined}
      aria-label={decorative ? undefined : t(`status.${status}`)}
      title={t(`status.${status}`)}
    />
  )
}
