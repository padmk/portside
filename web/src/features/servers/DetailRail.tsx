import type { ServerView } from '../../api/types'
import { useT } from '../../i18n'
import { selectServer } from '../../state/route'
import { displayStatus, Lamp } from '../../ui/Lamp'
import { buildSections } from '../overview/overviewState'

interface Props {
  servers: ServerView[]
  groups: string[]
  selected: string
}

/** Every server, grouped, for switching without going back to the overview. */
export function DetailRail({ servers, groups, selected }: Props) {
  const t = useT()
  const sections = buildSections(servers, groups, '', null)
  const titled = servers.some((s) => s.group)
  return (
    <nav className="rail" aria-label={t('detail.rail')}>
      {sections.map((s) => (
        <div key={s.group ?? '\0'} className="rail-group">
          {titled && <h2 className="rail-title">{s.group ?? t('list.ungrouped')}</h2>}
          <ul>
            {s.shown.map((v) => {
              const status = displayStatus(v)
              return (
                <li key={v.name}>
                  <button type="button" className="rail-item" aria-current={v.name === selected ? 'page' : undefined} onClick={() => selectServer(v.name)}>
                    <Lamp status={status} />
                    <span className="rail-name">{v.name}</span>
                    <span className="sr-only">{t(`status.${status}`)}</span>
                    {v.port > 0 && <span className="rail-port mono">{v.port}</span>}
                  </button>
                </li>
              )
            })}
          </ul>
        </div>
      ))}
    </nav>
  )
}
