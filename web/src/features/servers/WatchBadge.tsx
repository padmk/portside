import type { ServerView } from '../../api/types'
import { useT } from '../../i18n'

/** Marks a server that restarts when its watched files change; the hint lists the patterns. */
export function WatchBadge({ server: v }: { server: ServerView }) {
  const t = useT()
  if (!v.watch?.length) return null
  return (
    <span className="chip" title={t('badge.watchHint', { patterns: v.watch.join(', ') })}>
      {t('badge.watch')}
    </span>
  )
}
