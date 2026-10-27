import type { ServerView } from '../../api/types'
import { useT } from '../../i18n'
import { ServerNetworkBadge } from './NetworkBadge'
import { ScheduleBadge } from './ScheduleBadge'
import { TaskBadge } from './TaskBadge'
import { TemporaryBadge } from './TemporaryBadge'

/** The chips a server's card and list row show next to its name, in order. */
export function ServerBadges({ server: v, now }: { server: ServerView; now: number }) {
  const t = useT()
  return (
    <>
      {v.stale && <span className="chip chip--warn" title={t('badge.staleHint')}>{t('badge.stale')}</span>}
      {!v.enabled && <span className="chip">{t('badge.disabled')}</span>}
      <TaskBadge server={v} />
      <ScheduleBadge server={v} now={now} />
      <TemporaryBadge server={v} />
      <ServerNetworkBadge server={v} />
    </>
  )
}
