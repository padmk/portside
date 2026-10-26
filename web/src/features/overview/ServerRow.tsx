import { ExternalLink, Play, RotateCw, Square, Unplug } from 'lucide-react'
import type { ServerView } from '../../api/types'
import { useT } from '../../i18n'
import { useLive } from '../../state/live'
import { selectServer } from '../../state/route'
import { displayStatus, Lamp } from '../../ui/Lamp'
import { Sparkline } from '../../ui/charts'
import { restartServer, startServer, stopServer, useBusy } from '../servers/actions'
import { bareUrl, formatBytes, formatCpu, formatUptime, hasProcess, isUp, restartPending } from '../servers/format'
import { useProtectedReason } from '../servers/holder'
import { isDegraded, useServerNote } from '../servers/note'
import { ProjectTile } from '../servers/ProjectTile'
import { TemporaryBadge } from '../servers/TemporaryBadge'
import { ServerNetworkBadge } from '../servers/NetworkBadge'
import { ContainerCount } from '../containers/ContainerStrip'
import { containersOf } from '../containers/status'

interface Props {
  server: ServerView
  now: number
  cursor: boolean
}

/** The dense row for the list layout: everything on one line, actions at the end. */
export function ServerRow({ server: v, now, cursor }: Props) {
  const t = useT()
  const busy = useBusy(v.name)
  const status = displayStatus(v)
  const alive = hasProcess(v)
  const pending = restartPending(v)
  const reachable = isUp(v)
  const reason = useProtectedReason(v.external?.pid, v.port)
  const note = useServerNote(v)
  const lastLine = v.tail?.[v.tail.length - 1] ?? ''
  const open = () => selectServer(v.name)
  const containers = containersOf(useLive().state?.docker, v.name)

  return (
    <li className="row" data-status={status} data-cursor={cursor || undefined} data-card-name={v.name}>
      <Lamp status={status} decorative large />
      <ProjectTile server={v} size={36} />
      <div className="row-id">
        <h3 className="row-name">
          <button type="button" className="card-open" onClick={open} aria-label={t('card.open', { name: v.name })}>
            {v.name}
          </button>
          {v.stale && <span className="chip chip--warn" title={t('badge.staleHint')}>{t('badge.stale')}</span>}
          {!v.enabled && <span className="chip">{t('badge.disabled')}</span>}
          <TemporaryBadge server={v} />
          <ServerNetworkBadge server={v} />
          <ContainerCount server={v} containers={containers} />
        </h3>
        <p className="row-sub">
          <span className={`status-text--${status}`}>{t(`status.${status}`)}</span>
          {alive && v.startedAt && <span className="num dim">{formatUptime(v.startedAt, now)}</span>}
          {note && <span className={isDegraded(v, note) ? 'status-text--starting row-note' : 'dim row-note'}>{note}</span>}
          {!note && v.forwardedFrom && <span className="dim row-note">{t('banner.forwarded', { port: v.port, computer: v.forwardedFrom })}</span>}
        </p>
      </div>
      <div className="row-addr mono">
        {v.url ? (
          reachable ? (
            <a href={v.url} target="_blank" rel="noreferrer" title={t('common.openUrl', { url: v.url })}>
              {bareUrl(v.url)}
              <ExternalLink size={14} aria-hidden="true" />
            </a>
          ) : (
            <span className="dim">{bareUrl(v.url)}</span>
          )
        ) : (
          <span className="dim">{t('detail.noPort')}</span>
        )}
      </div>
      <p className="row-log mono" title={lastLine}>{lastLine}</p>
      <div className="row-stats">
        {alive ? (
          <>
            <Sparkline values={v.cpuHistory ?? []} className="stat-spark" width={64} height={24} />
            <span className="num">{formatCpu(v.cpu)}</span>
            <span className="num">{formatBytes(v.mem)}</span>
          </>
        ) : (
          <span className="dim">–</span>
        )}
      </div>
      <div className="row-actions">
        {alive || pending ? (
          <>
            {reachable && v.url && (
              <a className="icon-btn icon-btn--boxed" href={v.url} target="_blank" rel="noreferrer" aria-label={t('common.openUrl', { url: v.url })} title={t('common.open')}>
                <ExternalLink size={16} />
              </a>
            )}
            <button type="button" className="icon-btn icon-btn--boxed" aria-label={t('action.restartName', { name: v.name })} title={t('action.restart')} disabled={busy || v.status === 'stopping'} onClick={() => restartServer(v.name)}>
              <RotateCw size={16} />
            </button>
            <button type="button" className="icon-btn icon-btn--boxed" aria-label={t(v.temporary ? 'action.stopRemoveName' : 'action.stopName', { name: v.name })} title={t(v.temporary ? 'action.stopRemove' : 'action.stop')} disabled={busy || v.status === 'stopping'} onClick={() => stopServer(v.name)}>
              <Square size={14} fill="currentColor" />
            </button>
          </>
        ) : v.external ? (
          <button type="button" className="btn btn--primary btn--sm" disabled={busy || reason !== null} onClick={() => startServer(v.name, true)} title={reason ? t('banner.protected', { reason }) : undefined}>
            <Unplug size={15} aria-hidden="true" />
            {t('card.takeOver')}
          </button>
        ) : (
          <button type="button" className="btn btn--primary btn--sm" disabled={busy} onClick={() => startServer(v.name)} aria-label={t(status === 'crashed' ? 'action.restartName' : 'action.startName', { name: v.name })}>
            {status === 'crashed' ? <RotateCw size={15} aria-hidden="true" /> : <Play size={14} fill="currentColor" aria-hidden="true" />}
            {t(status === 'crashed' ? 'action.restart' : 'action.start')}
          </button>
        )}
      </div>
    </li>
  )
}
