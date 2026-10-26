import { ExternalLink, Play, RotateCw, Square, SquareTerminal, Unplug } from 'lucide-react'
import type { ReactNode } from 'react'
import type { ServerView } from '../../api/types'
import { useT } from '../../i18n'
import { selectServer } from '../../state/route'
import { showTerminal } from '../../state/ui'
import { displayStatus } from '../../ui/Lamp'
import { restartServer, startServer, stopServer, useBusy } from './actions'
import { restartPending } from './format'
import { useProtectedReason } from './holder'

interface Props {
  server: ServerView
  /** Hides "View logs" where the terminal is already on screen. */
  inDetail?: boolean
}

export function viewLogs(name: string) {
  selectServer(name)
  showTerminal()
}

/**
 * One clear primary action per state, with the rest as icon buttons:
 * stopped starts, running opens, crashed restarts, a held port is taken over.
 * A server waiting for its automatic restart can also be stopped.
 */
export function StateActions({ server: v, inDetail }: Props) {
  const t = useT()
  const busy = useBusy(v.name)
  const status = displayStatus(v)
  const reason = useProtectedReason(v.external?.pid, v.port)

  const logs = !inDetail && (
    <button type="button" className="btn btn--lg" onClick={() => viewLogs(v.name)}>
      <SquareTerminal size={17} aria-hidden="true" />
      {t('card.viewLogs')}
    </button>
  )
  const restart = (
    <button type="button" className="icon-btn icon-btn--boxed icon-btn--lg" aria-label={t('action.restartName', { name: v.name })} title={t('action.restart')} disabled={busy} onClick={() => restartServer(v.name)}>
      <RotateCw size={18} />
    </button>
  )
  const stop = (
    <button type="button" className="icon-btn icon-btn--boxed icon-btn--lg" aria-label={t(v.temporary ? 'action.stopRemoveName' : 'action.stopName', { name: v.name })} title={t(v.temporary ? 'action.stopRemove' : 'action.stop')} disabled={busy} onClick={() => stopServer(v.name)}>
      <Square size={15} fill="currentColor" />
    </button>
  )
  const primary = (onClick: () => void, icon: ReactNode, label: string, disabled = busy) => (
    <button type="button" className="btn btn--primary btn--lg" disabled={disabled} onClick={onClick}>
      {icon}
      {label}
    </button>
  )
  const progress = (label: string) => (
    <div className="progress" role="status">
      <span className="progress-label">{label}</span>
      <span className="progress-bar" aria-hidden="true" />
    </div>
  )

  if (restartPending(v)) {
    return (
      <div className="actions">
        {primary(() => void startServer(v.name), <RotateCw size={17} aria-hidden="true" />, t('action.restart'))}
        {logs}
        <span className="actions-spacer" />
        {stop}
      </div>
    )
  }

  switch (status) {
    case 'running':
    case 'unready':
      return (
        <div className="actions">
          {v.url ? (
            <a className="btn btn--primary btn--lg" href={v.url} target="_blank" rel="noreferrer">
              {t('common.open')}
              <ExternalLink size={17} aria-hidden="true" />
            </a>
          ) : (
            (logs || <span className="actions-note">{t('detail.noPort')}</span>)
          )}
          <span className="actions-spacer" />
          {restart}
          {stop}
        </div>
      )
    case 'starting':
      return (
        <div className="actions">
          {progress(t('card.starting'))}
          {stop}
        </div>
      )
    case 'stopping':
      return <div className="actions">{progress(t('card.stopping'))}</div>
    case 'crashed':
      return (
        <div className="actions">
          {primary(() => void startServer(v.name), <RotateCw size={17} aria-hidden="true" />, t('action.restart'))}
          {logs}
        </div>
      )
    case 'blocked':
    case 'external': {
      let main: ReactNode
      if (!v.external) main = primary(() => void startServer(v.name), <Play size={16} fill="currentColor" aria-hidden="true" />, t('action.start'))
      else if (reason === null) main = primary(() => void startServer(v.name, true), <Unplug size={17} aria-hidden="true" />, t('card.takeOver'))
      else if (reason) main = <span className="actions-note">{t('card.protected')}</span>
      else main = primary(() => {}, <Unplug size={17} aria-hidden="true" />, t('card.takeOver'), true)
      return (
        <div className="actions">
          {main}
          {logs}
        </div>
      )
    }
    default:
      return <div className="actions">{primary(() => void startServer(v.name), <Play size={16} fill="currentColor" aria-hidden="true" />, t('action.start'))}</div>
  }
}
