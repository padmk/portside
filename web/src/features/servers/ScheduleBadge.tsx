import type { ServerView } from '../../api/types'
import { useT } from '../../i18n'
import { formatRunTime } from './format'

/** Marks a server its schedule starts, with the next run; the hint has the expression and the latest run. */
export function ScheduleBadge({ server: v, now = Date.now() }: { server: ServerView; now?: number }) {
  const t = useT()
  if (!v.schedule) return null
  const last = v.lastRun ? t('badge.scheduleLast', { time: formatRunTime(v.lastRun, now) }) : ''
  return (
    <span className="chip" title={[t('badge.scheduleHint', { schedule: v.schedule }), last].filter(Boolean).join(' ')}>
      {v.nextRun ? t('badge.scheduleNext', { time: formatRunTime(v.nextRun, now) }) : t('badge.schedule')}
    </span>
  )
}
