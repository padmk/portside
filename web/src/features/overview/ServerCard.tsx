import { AlertTriangle, ArrowLeftRight, Info, Plug } from 'lucide-react'
import type { ServerView } from '../../api/types'
import { useT } from '../../i18n'
import { useLive } from '../../state/live'
import { selectServer } from '../../state/route'
import { displayStatus, Lamp } from '../../ui/Lamp'
import { Sparkline } from '../../ui/charts'
import { Address } from '../servers/Address'
import { Charts } from '../servers/Charts'
import { formatAgo, formatBytes, formatCpu, formatUptime, hasProcess } from '../servers/format'
import { isDegraded, useServerNote } from '../servers/note'
import { ProjectTile } from '../servers/ProjectTile'
import { TemporaryBadge } from '../servers/TemporaryBadge'
import { ServerMenu } from '../servers/ServerMenu'
import { StateActions } from '../servers/StateActions'
import { ContainerStrip } from '../containers/ContainerStrip'
import { containerIssues, containersOf, containerStatus, shortName } from '../containers/status'
import { LogPreview } from './LogPreview'
import type { Density } from './overviewState'

const LOG_LINES: Record<Density, number> = { solo: 6, roomy: 5, cozy: 4, compact: 3 }

interface Props {
  server: ServerView
  density: Density
  now: number
  cursor: boolean
}

export function ServerCard({ server: v, density, now, cursor }: Props) {
  const t = useT()
  const status = displayStatus(v)
  const alive = hasProcess(v)
  const cpu = v.cpuHistory ?? []
  const mem = v.memHistory ?? []

  const note = useServerNote(v)
  const degraded = isDegraded(v, note)
  const held = v.external
    ? t('banner.portUsed', { port: v.port, process: v.external.command, pid: v.external.pid })
    : v.forwardedFrom
      ? t('banner.forwarded', { port: v.port, computer: v.forwardedFrom })
      : ''
  const ago =!alive ? formatAgo(v.exitedAt, now) : ''
  const containers = containersOf(useLive().state?.docker, v.name)
  const issue = containerIssues(v, containers)[0]

  return (
    <div className="card-cell">
      <article className="card" data-status={status} data-density={density} data-alive={alive || undefined} data-cursor={cursor || undefined} data-card-name={v.name}>
        <div className="card-main">
          <header className="card-head">
            <ProjectTile server={v} size={density === 'solo' ? 56 : 48} />
            <div className="card-title">
              <h3>
                <button type="button" className="card-open" onClick={() => selectServer(v.name)} aria-label={t('card.open', { name: v.name })}>
                  {v.name}
                </button>
              </h3>
              <p className={`card-state status-text--${status}`}>
                <Lamp status={status} decorative large />
                <span className="state-label">{t(`status.${status}`)}</span>
                {alive && v.startedAt && <span className="state-time num">{formatUptime(v.startedAt, now)}</span>}
                {!alive && ago && <span className="state-time">{t('card.ago', { ago })}</span>}
              </p>
            </div>
            <div className="card-flags">
              {v.stale && <span className="chip chip--warn" title={t('badge.staleHint')}>{t('badge.stale')}</span>}
              {!v.enabled && <span className="chip">{t('badge.disabled')}</span>}
              <TemporaryBadge server={v} />
            </div>
            <ServerMenu server={v} className="icon-btn icon-btn--lg card-menu" />
          </header>

          <div className="card-addr">
            <Address server={v} />
          </div>
          <ContainerStrip server={v} containers={containers} />

          {density === 'solo' && alive && (
            <dl className="card-vitals">
              <div>
                <dt>{t('detail.uptime')}</dt>
                <dd className="num">{v.startedAt ? formatUptime(v.startedAt, now) : '–'}</dd>
              </div>
              <div>
                <dt>{t('detail.pid')}</dt>
                <dd className="num mono">{v.pid ?? '–'}</dd>
              </div>
              <div>
                <dt>{t('detail.restarts')}</dt>
                <dd className="num">{v.restarts ?? 0}</dd>
              </div>
            </dl>
          )}
          <div className="card-stats">
            {alive ? (
              <>
                <span className="stat">
                  <span className="stat-label">{t('detail.cpu')}</span>
                  <Sparkline values={cpu} className="stat-spark stat-spark--cpu" width={88} height={26} />
                  <span className="num stat-value">{formatCpu(v.cpu)}</span>
                </span>
                <span className="stat">
                  <span className="stat-label">{t('detail.memory')}</span>
                  <Sparkline values={mem} className="stat-spark stat-spark--mem" width={88} height={26} />
                  <span className="num stat-value">{formatBytes(v.mem)}</span>
                </span>
                {v.pid ? (
                  <span className="stat stat--pid">
                    <span className="stat-label">{t('detail.pid')}</span>
                    <span className="num stat-value mono">{v.pid}</span>
                  </span>
                ) : null}
              </>
            ) : (
              <span className="stat-idle">{t('card.notRunning')}</span>
            )}
          </div>

          <p className="card-cmd mono" title={v.cmd}>
            <span aria-hidden="true">$ </span>
            {v.cmd}
          </p>
        </div>

        <div className="card-side">
          <LogPreview lines={v.tail ?? []} count={LOG_LINES[density]} dim={!alive} />
          {density === 'solo' && alive && (
            <div className="card-charts">
              <Charts server={v} />
            </div>
          )}
        </div>

        <div className="card-foot">
          {note && (
            <p className={`note note--${degraded ? 'starting' : status}`}>
              {status === 'crashed' || status === 'blocked' || degraded ? (
                <AlertTriangle size={16} aria-hidden="true" />
              ) : v.forwardedFrom && !alive ? (
                <ArrowLeftRight size={16} aria-hidden="true" />
              ) : (
                <Info size={16} aria-hidden="true" />
              )}
              <span>{note}</span>
            </p>
          )}
          {issue && !note && (
            <p className="note note--crashed">
              <AlertTriangle size={16} aria-hidden="true" />
              <span>{t('containers.issueOne', { name: shortName(issue), status: t(containerStatus(issue).label) })}</span>
            </p>
          )}
          {held && !note && !issue && (
            <p className="note note--external">
              <Plug size={16} aria-hidden="true" />
              <span>{held}</span>
            </p>
          )}
          <StateActions server={v} />
        </div>
      </article>
    </div>
  )
}
