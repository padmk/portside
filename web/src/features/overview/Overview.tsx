import { useEffect } from 'react'
import { ChevronDown, ChevronRight, Play, RotateCw, Square, X } from 'lucide-react'
import type { BulkAction, ServerView } from '../../api/types'
import { useT } from '../../i18n'
import { useNow } from '../../ui/hooks'
import { bulk, groupKey, groupTargets, useBusy } from '../servers/actions'
import { ServerCard } from './ServerCard'
import { ServerRow } from './ServerRow'
import {
  buildSections,
  clearFilters,
  densityFor,
  layoutFor,
  toggleGroup,
  useOverviewState,
  type Density,
  type Layout,
  type Section,
} from './overviewState'
import './overview.css'

interface Props {
  servers: ServerView[]
  groups: string[]
}

export function Overview({ servers, groups }: Props) {
  const t = useT()
  const { filter, status, collapsed, layoutChoice, cursor } = useOverviewState()
  const now = useNow(1000)
  const layout = layoutFor(layoutChoice, servers.length)
  const density = densityFor(servers.length)
  const filtering = filter.trim() !== '' || status !== null
  const sections = buildSections(servers, groups, filter, status)
  const titled = servers.some((s) => s.group)
  const shown = sections.reduce((n, s) => n + s.shown.length, 0)

  useEffect(() => {
    if (cursor) document.querySelector(`[data-card-name="${CSS.escape(cursor)}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [cursor])

  return (
    <section className="overview" aria-label={t('list.title')}>
      <div className="overview-inner">
        {filtering && (
          <div className="filter-note" role="status">
            <span>{t('list.showing', { shown, total: servers.length })}</span>
            <button type="button" className="btn btn--sm" onClick={clearFilters}>
              <X size={14} aria-hidden="true" />
              {t('list.clearFilters')}
            </button>
          </div>
        )}
        {sections.length === 0 && <p className="empty-note">{t('list.noMatch', { q: filter || (status ? t(`status.${status}`) : '') })}</p>}
        {sections.map((s) => (
          <GroupSection
            key={s.group ?? '\0'}
            section={s}
            titled={titled}
            collapsed={!filtering && collapsed.has(s.group ?? '')}
            layout={layout}
            density={density}
            now={now}
            cursor={cursor}
          />
        ))}
      </div>
    </section>
  )
}

interface SectionProps {
  section: Section
  titled: boolean
  collapsed: boolean
  layout: Layout
  density: Density
  now: number
  cursor: string | null
}

function GroupSection({ section, titled, collapsed, layout, density, now, cursor }: SectionProps) {
  const t = useT()
  const key = section.group ?? ''
  const title = section.group ?? t('list.ungrouped')
  const running = section.all.filter((s) => s.status === 'running').length
  const busy = useBusy(`bulk:${groupKey(key)}`)
  const targets = groupTargets(section.all)
  const act = (action: BulkAction) => bulk(groupKey(key), action, { names: targets[action] })
  const bodyId = `group-${key || 'other'}`

  return (
    <div className="group">
      {titled && (
        <div className="group-head">
          <button type="button" className="group-toggle" aria-expanded={!collapsed} aria-controls={bodyId} onClick={() => toggleGroup(key)}>
            {collapsed ? <ChevronRight size={18} aria-hidden="true" /> : <ChevronDown size={18} aria-hidden="true" />}
            <h2 className="group-name">{title}</h2>
            <span className="group-count">{t('list.groupCount', { n: section.all.length, r: running })}</span>
          </button>
          <div className="group-actions">
            <button type="button" className="btn btn--sm" aria-label={t('list.startGroup', { name: title })} disabled={busy || targets.start.length === 0} onClick={() => act('start')}>
              <Play size={13} fill="currentColor" aria-hidden="true" />
              {t('menu.startAll')}
            </button>
            <button type="button" className="btn btn--sm" aria-label={t('list.restartGroup', { name: title })} disabled={busy || targets.restart.length === 0} onClick={() => act('restart')}>
              <RotateCw size={13} aria-hidden="true" />
              {t('menu.restartAll')}
            </button>
            <button type="button" className="btn btn--sm" aria-label={t('list.stopGroup', { name: title })} disabled={busy || targets.stop.length === 0} onClick={() => act('stop')}>
              <Square size={12} fill="currentColor" aria-hidden="true" />
              {t('menu.stopAll')}
            </button>
          </div>
        </div>
      )}
      {!collapsed &&
        (layout === 'cards' ? (
          <div className="cards" id={bodyId} data-density={density}>
            {section.shown.map((v) => (
              <ServerCard key={v.name} server={v} density={density} now={now} cursor={cursor === v.name} />
            ))}
          </div>
        ) : (
          <ul className="rows" id={bodyId}>
            {section.shown.map((v) => (
              <ServerRow key={v.name} server={v} now={now} cursor={cursor === v.name} />
            ))}
          </ul>
        ))}
    </div>
  )
}
