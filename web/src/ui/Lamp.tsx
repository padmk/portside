import { useT } from '../i18n'
import type { ServerView } from '../api/types'

/**
 * The status a server is displayed with: `external` is a stopped server whose
 * port is held by another process, `unready` a running one that stopped
 * answering on its port or health path, `done` a task whose last run succeeded.
 */
export type DisplayStatus = 'running' | 'unready' | 'starting' | 'stopping' | 'crashed' | 'blocked' | 'stopped' | 'exited' | 'done' | 'external'

export function displayStatus(v: Pick<ServerView, 'status' | 'external' | 'unready' | 'done'>): DisplayStatus {
  if (v.status === 'stopped' && v.external) return 'external'
  if (v.status === 'running' && v.unready) return 'unready'
  if (v.status === 'exited' && v.done) return 'done'
  return v.status
}

interface LampProps {
  status: DisplayStatus
  /** Replaces the status name as the accessible label and tooltip. */
  label?: string
  className?: string
  decorative?: boolean
  large?: boolean
}

export function Lamp({ status, label, className = '', decorative, large }: LampProps) {
  const t = useT()
  const text = label ?? t(`status.${status}`)
  return (
    <span
      className={`lamp lamp--${status}${large ? ' lamp--lg' : ''} ${className}`}
      role={decorative ? undefined : 'img'}
      aria-hidden={decorative || undefined}
      aria-label={decorative ? undefined : text}
      title={text}
    />
  )
}
