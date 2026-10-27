import type { ServerView } from '../../api/types'
import { useT } from '../../i18n'

/** Marks a task (`oneshot = true`), which runs to completion instead of serving. */
export function TaskBadge({ server: v }: { server: ServerView }) {
  const t = useT()
  if (!v.oneshot) return null
  return (
    <span className="chip" title={t('badge.taskHint')}>
      {t('badge.task')}
    </span>
  )
}
