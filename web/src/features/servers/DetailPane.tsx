import { lazy, Suspense } from 'react'
import { AlertTriangle, ArrowLeft } from 'lucide-react'
import type { ServerView } from '../../api/types'
import { useT } from '../../i18n'
import { translateNote } from '../../i18n/notes'
import { usePrefs } from '../../state/prefs'
import { closeServer } from '../../state/route'
import { setTab, useUi } from '../../state/ui'
import { displayStatus, Lamp } from '../../ui/Lamp'
import { useNow } from '../../ui/hooks'
import { restartServer, useBusy } from './actions'
import { Address } from './Address'
import { DetailsTab } from './DetailsTab'
import { formatUptime, hasProcess } from './format'
import { PortBanner } from './PortBanner'
import { ProjectTile } from './ProjectTile'
import { ServerMenu } from './ServerMenu'
import { StateActions } from './StateActions'
import './detail.css'

const TerminalPane = lazy(() => import('../terminal/TerminalPane').then((m) => ({ default: m.TerminalPane })))

interface Props {
  server: ServerView
  servers: ServerView[]
}

export function DetailPane({ server: v, servers }: Props) {
  const t = useT()
  const { tab } = useUi()
  const busy = useBusy(v.name)
  const now = useNow(1000)
  const status = displayStatus(v)
  const alive = hasProcess(v)
  const { locale } = usePrefs()
  const note = v.note && v.note.toLowerCase() !== status ? translateNote(locale, v.note) : ''

  return (
    <section className="detail" aria-label={t('detail.title', { name: v.name })}>
      <header className="detail-head">
        <button type="button" className="icon-btn icon-btn--boxed icon-btn--lg" aria-label={t('detail.back')} title={t('detail.backHint')} onClick={closeServer}>
          <ArrowLeft size={19} />
        </button>
        <ProjectTile server={v} size={48} />
        <div className="detail-title">
          <h2>
            {v.group && <span className="grp">{v.group}<span aria-hidden="true"> / </span></span>}
            {v.name}
          </h2>
          <p className={`detail-status status-text--${status}`}>
            <Lamp status={status} decorative large />
            <span className="state-label">{t(`status.${status}`)}</span>
            {note && <span className="note-text">{note}</span>}
          </p>
        </div>
        <Address server={v} />
        <dl className="vitals">
          <div>
            <dt>{t('detail.pid')}</dt>
            <dd className="num mono">{v.pid ?? '–'}</dd>
          </div>
          <div>
            <dt>{t('detail.uptime')}</dt>
            <dd className="num">{alive && v.startedAt ? formatUptime(v.startedAt, now) : '–'}</dd>
          </div>
          <div>
            <dt>{t('detail.restarts')}</dt>
            <dd className="num">{v.restarts ?? 0}</dd>
          </div>
        </dl>
        <span className="spacer" />
        <StateActions server={v} inDetail />
        <ServerMenu server={v} className="icon-btn icon-btn--boxed icon-btn--lg" />
      </header>

      {v.external && <PortBanner server={v} />}
      {v.stale && alive && (
        <div className="banner banner--warn" role="status">
          <AlertTriangle size={18} aria-hidden="true" />
          <div className="body">
            <strong>{t('banner.staleTitle')}</strong> {t('banner.staleBody')}
          </div>
          <button type="button" className="btn" disabled={busy} onClick={() => restartServer(v.name)}>
            {t('action.restart')}
          </button>
        </div>
      )}

      <div className="tabs" role="tablist" aria-label={t('detail.title', { name: v.name })}>
        {(['terminal', 'details'] as const).map((id) => (
          <button
            key={id}
            type="button"
            role="tab"
            id={`tab-${id}`}
            className="tab"
            aria-selected={tab === id}
            aria-controls={`panel-${id}`}
            tabIndex={tab === id ? 0 : -1}
            onClick={() => setTab(id)}
            onKeyDown={(e) => {
              if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
                // Handled here: the global shortcuts must not also switch servers.
                e.preventDefault()
                e.stopPropagation()
                const next = id === 'terminal' ? 'details' : 'terminal'
                setTab(next)
                requestAnimationFrame(() => document.getElementById(`tab-${next}`)?.focus())
              }
            }}
          >
            {t(`tab.${id}`)}
          </button>
        ))}
      </div>
      <div className="tabpanel" role="tabpanel" id={`panel-${tab}`} aria-labelledby={`tab-${tab}`}>
        {tab === 'terminal' ? (
          <Suspense fallback={null}>
            <TerminalPane key={v.name} name={v.name} interactive={v.status === 'running' || v.status === 'starting'} />
          </Suspense>
        ) : (
          <DetailsTab server={v} servers={servers} />
        )}
      </div>
    </section>
  )
}
