import { useState } from 'react'
import { MoreHorizontal, Play, Square, Trash2 } from 'lucide-react'
import { Menu, MenuItem } from '../../ui/Menu'
import { ConfirmDialog } from '../dialogs/ConfirmDialog'
import { useBusy } from '../servers/actions'
import { projectAction, projectBusyKey } from './actions'
import type { ContainerView, DockerState } from '../../api/types'
import { useT } from '../../i18n'
import { selectServer } from '../../state/route'
import { pickContainer, useUi } from '../../state/ui'
import { Lamp } from '../../ui/Lamp'
import { useNow } from '../../ui/hooks'
import { formatAgo, formatBytes, formatCpu, formatUptime } from '../servers/format'
import { ContainerPanel } from './ContainerPanel'
import { EngineNotice } from './EngineNotice'
import { containerStatus, failed, isRunning, shortName } from './status'
import './containers.css'

interface Props {
  docker: DockerState
  containers: ContainerView[]
  /** Server name, or '' for the containers page: each view remembers its own selection. */
  scope: string
}

/** A list of containers beside the selected one's logs, shell and details. */
export function ContainersView({ docker, containers, scope }: Props) {
  const t = useT()
  const { picked } = useUi()
  if (docker.status !== 'ready') return <EngineNotice docker={docker} />
  if (containers.length === 0) return <p className="ct-none">{t('containers.none')}</p>

  const selected =
    containers.find((c) => c.name === picked[scope]) ??
    containers.find((c) => c.state === 'running' && c.health === 'unhealthy') ??
    containers.find(isRunning) ??
    containers[0]!

  return (
    <div className="ctv">
      <ContainerList containers={containers} selected={selected.name} scope={scope} />
      <ContainerPanel key={selected.name} container={selected} />
    </div>
  )
}

interface Group {
  project: string
  server?: string
  items: ContainerView[]
}

function groupsOf(containers: ContainerView[]): Group[] {
  const out: Group[] = []
  for (const c of containers) {
    const project = c.project ?? ''
    let g = out.find((x) => x.project === project)
    if (!g) {
      g = { project, server: c.server, items: [] }
      out.push(g)
    }
    g.items.push(c)
  }
  // Projects first, loose containers last.
  return out.sort((a, b) => (a.project === '') === (b.project === '') ? 0 : a.project === '' ? 1 : -1)
}

function ContainerList({ containers, selected, scope }: { containers: ContainerView[]; selected: string; scope: string }) {
  const t = useT()
  const now = useNow(1000)
  const groups = groupsOf(containers)
  const titled = groups.length > 1 || scope === ''
  return (
    <nav className="ct-list" aria-label={t('containers.title')}>
      {groups.map((g) => (
        <div key={g.project || '\0'} className="ct-group">
          {(titled || g.items.some((c) => c.project && c.service && c.workingDir && !c.oneOff)) && (
            <div className="ct-group-title">
              <span className="ct-group-name">{g.project || t('containers.loose')}</span>
              {g.server && scope !== g.server && (
                <button type="button" className="chip chip--link" onClick={() => selectServer(g.server!)} title={t('containers.openServer', { name: g.server })}>
                  {g.server}
                </button>
              )}
              <span className="spacer" />
              <ProjectActions group={g} />
            </div>
          )}
          <ul>
            {g.items.map((c) => {
              const st = containerStatus(c)
              const running = c.state === 'running'
              const when = running ? formatUptime(c.startedAt, now) : c.finishedAt ? t('card.ago', { ago: formatAgo(c.finishedAt, now) }) : ''
              return (
                <li key={c.id}>
                  <button
                    type="button"
                    className="ct-item"
                    aria-current={c.name === selected ? 'true' : undefined}
                    onClick={() => pickContainer(scope, c.name)}
                  >
                    <Lamp status={st.lamp} label={t(st.label)} />
                    <span className="ct-main">
                      <span className="ct-name">{shortName(c)}</span>
                      <span className={`ct-sub${st.lamp === 'crashed' ? ' status-text--crashed' : ''}`}>
                        {t(st.label)}
                        {failed(c) && c.exitCode !== undefined && ` (${c.exitCode})`}
                        {when && <span className="dim num"> · {when}</span>}
                      </span>
                    </span>
                    {running && (
                      <span className="ct-usage num">
                        <span>{formatCpu(c.cpu)}</span>
                        <span>{formatBytes(c.mem)}</span>
                      </span>
                    )}
                    {c.job?.running && <span className="ct-busy" aria-label={t(`job.${c.job.kind}Running`)} title={t(`job.${c.job.kind}Running`)} />}
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

function ProjectActions({ group: g }: { group: Group }) {
  const t = useT()
  const busy = useBusy(projectBusyKey(g.project))
  const [confirm, setConfirm] = useState(false)
  const c = g.items.find((item) => item.project && item.service && item.workingDir && !item.oneOff)
  if (!c) return null
  const disabled = busy || g.items.some((item) => item.job?.running)
  return (
    <>
      <Menu label={t('containers.projectActions', { name: g.project })} trigger={<MoreHorizontal size={16} />}>
        <MenuItem icon={<Play size={14} />} disabled={disabled} onSelect={() => { void projectAction(c.name, g.project, 'start') }}>{t('containers.startAll')}</MenuItem>
        <MenuItem icon={<Square size={14} />} disabled={disabled} onSelect={() => { void projectAction(c.name, g.project, 'stop') }}>{t('containers.stopAll')}</MenuItem>
        <MenuItem icon={<Trash2 size={14} />} danger disabled={disabled} onSelect={() => setConfirm(true)}>{t('containers.remove')}</MenuItem>
      </Menu>
      {confirm && <ConfirmDialog
        title={t('containers.remove')}
        message={t('containers.removeConfirm', { name: g.project })}
        confirmLabel={t('containers.remove')}
        onConfirm={() => projectAction(c.name, g.project, 'down', true)}
        onClose={() => setConfirm(false)}
      />}
    </>
  )
}
