import { Container } from 'lucide-react'
import type { ContainerView, ServerView } from '../../api/types'
import { useT } from '../../i18n'
import { selectServer } from '../../state/route'
import { pickContainer, setTab } from '../../state/ui'
import { Lamp } from '../../ui/Lamp'
import { containerIssues, containerStatus, shortName } from './status'

/** Opens a server's containers tab on one container. */
export function showContainer(server: string, name: string) {
  pickContainer(server, name)
  selectServer(server)
  setTab('containers')
}

const MAX = 4

/** A server's containers as small status items on its card. */
export function ContainerStrip({ server: v, containers }: { server: ServerView; containers: ContainerView[] }) {
  const t = useT()
  if (containers.length === 0) return null
  const issues = new Set(containerIssues(v, containers).map((c) => c.name))
  // Containers that need attention come first, so they survive the cut.
  const shown = [...containers].sort((a, b) => Number(issues.has(b.name)) - Number(issues.has(a.name))).slice(0, MAX)
  const more = containers.length - shown.length
  return (
    <ul className="ct-strip" aria-label={t('containers.title')}>
      <li aria-hidden="true" className="ct-strip-icon">
        <Container size={15} />
      </li>
      {shown.map((c) => {
        const st = containerStatus(c)
        return (
          <li key={c.id}>
            <button
              type="button"
              className={`ct-pill${issues.has(c.name) ? ' ct-pill--issue' : ''}`}
              title={`${c.name}: ${t(st.label)}`}
              aria-label={t('containers.showOne', { name: shortName(c), status: t(st.label) })}
              onClick={() => showContainer(v.name, c.name)}
            >
              <Lamp status={st.lamp} label={t(st.label)} decorative />
              {shortName(c)}
            </button>
          </li>
        )
      })}
      {more > 0 && (
        <li>
          <button type="button" className="ct-pill" onClick={() => showContainer(v.name, '')}>
            {t('containers.more', { n: more })}
          </button>
        </li>
      )}
    </ul>
  )
}

/** The list row's compact form: a count that turns into a warning when something needs attention. */
export function ContainerCount({ server: v, containers }: { server: ServerView; containers: ContainerView[] }) {
  const t = useT()
  if (containers.length === 0) return null
  const issues = containerIssues(v, containers)
  const running = containers.filter((c) => c.state === 'running').length
  const label = issues.length
    ? t('containers.issues', { n: issues.length })
    : t('containers.runningOf', { r: running, n: containers.length })
  return (
    <button
      type="button"
      className={`ct-count${issues.length ? ' ct-count--issue' : ''}`}
      title={issues.length ? issues.map((c) => c.name).join(', ') : label}
      aria-label={label}
      onClick={() => showContainer(v.name, issues[0]?.name ?? '')}
    >
      <Container size={14} aria-hidden="true" />
      <span className="num">{issues.length ? issues.length : `${running}/${containers.length}`}</span>
    </button>
  )
}
