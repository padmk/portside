import type { ServerView } from '../../api/types'
import { useT } from '../../i18n'

/** Marks a temporary copy of a configured server that runs in another worktree. */
export function TemporaryBadge({ server: v }: { server: ServerView }) {
  const t = useT()
  if (!v.temporary) return null
  return (
    <span className="chip chip--info" title={t('badge.worktreeHint', { origin: v.origin ?? '', dir: v.worktree ?? '' })}>
      {t('badge.worktree')}
    </span>
  )
}
