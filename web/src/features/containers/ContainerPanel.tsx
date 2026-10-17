import { lazy, Suspense, useState } from 'react'
import { ExternalLink, Hammer, MoreHorizontal, Play, RefreshCcw, RotateCw, Square, X } from 'lucide-react'
import { api } from '../../api/client'
import type { ContainerView } from '../../api/types'
import { useT } from '../../i18n'
import { translateNote } from '../../i18n/notes'
import { isMirrored, useRemote } from '../../state/live'
import { usePrefs } from '../../state/prefs'
import { setContainerTab, useUi, type ContainerTab } from '../../state/ui'
import { Lamp } from '../../ui/Lamp'
import { Menu, MenuItem } from '../../ui/Menu'
import { useNow } from '../../ui/hooks'
import { useBusy } from '../servers/actions'
import { CopyButton } from '../servers/Address'
import { formatAgo, formatUptime } from '../servers/format'
import { busyKey, containerAction, dismissJob, recreate } from './actions'
import { ContainerDetailsTab } from './ContainerDetailsTab'
import { containerStatus, failed } from './status'

const TerminalPane = lazy(() => import('../terminal/TerminalPane').then((m) => ({ default: m.TerminalPane })))

/** One container: what it is doing, its actions, and its logs, shell and details. */
export function ContainerPanel({ container: c }: { container: ContainerView }) {
  const t = useT()
  const now = useNow(1000)
  const { containerTab } = useUi()
  const { locale } = usePrefs()
  const busy = useBusy(busyKey(c.name))
  const st = containerStatus(c)
  const running = c.state === 'running'
  const jobRunning = !!c.job?.running
  const tabs: ContainerTab[] = ['logs', 'shell', 'details', ...(c.job ? (['job'] as const) : [])]
  const tab = tabs.includes(containerTab) ? containerTab : 'logs'
  const web = (c.ports ?? []).filter((p) => p.proto === 'tcp')
  const remote = useRemote()
  // On another computer, localhost:<port> opens this computer's service unless the port is forwarded.
  const opensHere = (port: number) => !remote || isMirrored(remote, port)
  const portHint = (p: { public: number; private: number }) =>
    opensHere(p.public)
      ? t('container.portHint', { public: p.public, private: p.private })
      : t('container.portRemote', { public: p.public, private: p.private, name: remote?.peer.name ?? '' })

  let when = ''
  if (running && c.startedAt) when = t('container.upFor', { time: formatUptime(c.startedAt, now) })
  else if (c.finishedAt) when = t('card.ago', { ago: formatAgo(c.finishedAt, now) })

  const rebuild = (build: boolean, pull = false) => {
    void recreate(c.name, build, pull).then((r) => r && setContainerTab('job'))
  }

  return (
    <section className="ct-panel" aria-label={t('container.title', { name: c.name })}>
      <header className="ct-head">
        <div className="ct-title">
          <h3>
            {c.name}
            <CopyButton text={c.name} label={t('container.copyName')} />
          </h3>
          <p className={`ct-state status-text--${st.lamp}`}>
            <Lamp status={st.lamp} label={t(st.label)} decorative large />
            <span className="state-label">{t(st.label)}</span>
            {failed(c) && c.exitCode !== undefined && <span className="chip chip--bad">{t('container.exitCode', { code: c.exitCode })}</span>}
            {c.oomKilled && <span className="chip chip--bad">{t('container.oom')}</span>}
            {when && <span className="ct-when num">{when}</span>}
          </p>
          <p className="ct-image mono" title={c.image}>{c.image}</p>
        </div>
        {web.length > 0 && (
          <ul className="ct-ports" aria-label={t('container.ports')}>
            {web.map((p) => (
              <li key={`${p.public}/${p.proto}`}>
                {running && opensHere(p.public) ? (
                  <a className="chip chip--mono chip--link" href={`http://localhost:${p.public}`} target="_blank" rel="noreferrer" title={portHint(p)}>
                    {p.public}
                    <ExternalLink size={12} aria-hidden="true" />
                  </a>
                ) : (
                  <span className="chip chip--mono" title={portHint(p)}>{p.public}</span>
                )}
              </li>
            ))}
          </ul>
        )}
        <span className="spacer" />
        <div className="ct-actions">
          {running || c.state === 'restarting' ? (
            <>
              <button type="button" className="icon-btn icon-btn--boxed" aria-label={t('action.restartName', { name: c.name })} title={t('action.restart')} disabled={busy || jobRunning} onClick={() => containerAction(c.name, 'restart')}>
                <RotateCw size={16} />
              </button>
              <button type="button" className="icon-btn icon-btn--boxed" aria-label={t('action.stopName', { name: c.name })} title={t('action.stop')} disabled={busy || jobRunning} onClick={() => containerAction(c.name, 'stop')}>
                <Square size={14} fill="currentColor" />
              </button>
            </>
          ) : (
            <button type="button" className="btn btn--primary" disabled={busy || jobRunning || c.state === 'removing'} onClick={() => containerAction(c.name, 'start')}>
              <Play size={14} fill="currentColor" aria-hidden="true" />
              {t('action.start')}
            </button>
          )}
          {c.project && c.service && c.workingDir && !c.oneOff && (
            <Menu label={t('detail.more')} trigger={<MoreHorizontal size={18} />} className="icon-btn icon-btn--boxed">
              <MenuItem icon={<RefreshCcw size={16} />} disabled={busy || jobRunning} onSelect={() => rebuild(false)}>{t('container.recreate')}</MenuItem>
              {c.buildable ? (
                <MenuItem icon={<Hammer size={16} />} disabled={busy || jobRunning} onSelect={() => rebuild(true)}>{t('container.rebuild')}</MenuItem>
              ) : (
                <MenuItem icon={<RefreshCcw size={16} />} disabled={busy || jobRunning} onSelect={() => rebuild(false, true)}>{t('container.pull')}</MenuItem>
              )}
            </Menu>
          )}
        </div>
      </header>

      <div className="tabs ct-tabs" role="tablist" aria-label={t('container.title', { name: c.name })}>
        {tabs.map((id) => (
          <button
            key={id}
            type="button"
            role="tab"
            id={`ctab-${id}`}
            className="tab"
            aria-selected={tab === id}
            aria-controls={`cpanel-${id}`}
            tabIndex={tab === id ? 0 : -1}
            onClick={() => setContainerTab(id)}
            onKeyDown={(e) => {
              if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return
              e.preventDefault()
              e.stopPropagation()
              const i = tabs.indexOf(id) + (e.key === 'ArrowRight' ? 1 : -1)
              const next = tabs[(i + tabs.length) % tabs.length]!
              setContainerTab(next)
              requestAnimationFrame(() => document.getElementById(`ctab-${next}`)?.focus())
            }}
          >
            {id === 'job' && c.job ? t(`job.${c.job.kind}`) : t(`container.tab.${id}`)}
            {id === 'job' && jobRunning && <span className="ct-busy" aria-hidden="true" />}
          </button>
        ))}
      </div>
      <div className="tabpanel" role="tabpanel" id={`cpanel-${tab}`} aria-labelledby={`ctab-${tab}`}>
        <Suspense fallback={null}>
          {tab === 'logs' && (
            <TerminalPane
              key={`logs:${c.name}`}
              source={api.containerLogsPath(c.name)}
              resume
              interactive={false}
              input="none"
              emptyText={t('container.noLogs')}
              waitingText={t('terminal.waiting')}
            />
          )}
          {tab === 'shell' && (running ? <Shell name={c.name} /> : <p className="ct-none">{t('container.shellOff')}</p>)}
          {tab === 'details' && <ContainerDetailsTab container={c} />}
          {tab === 'job' && c.job && (
            <>
              <div className={`banner ${c.job.error ? 'banner--bad' : 'banner--info'} ct-job`} role="status">
                <div className="body">
                  {c.job.running ? t(`job.${c.job.kind}Running`) : c.job.error ? t('job.failed', { error: translateNote(locale, c.job.error) }) : t('job.done')}
                </div>
                {!c.job.running && (
                  <button type="button" className="icon-btn" aria-label={t('common.dismiss')} title={t('common.dismiss')} onClick={() => dismissJob(c.name)}>
                    <X size={16} />
                  </button>
                )}
              </div>
              <TerminalPane
                key={`job:${c.name}:${c.job.startedAt}`}
                source={api.containerJobPath(c.name)}
                interactive={false}
                input="none"
                emptyText={t('terminal.waiting')}
                waitingText={t('terminal.waiting')}
              />
            </>
          )}
        </Suspense>
      </div>
    </section>
  )
}

/** A shell session; when it ends, a new one starts on request. */
function Shell({ name }: { name: string }) {
  const t = useT()
  const [session, setSession] = useState(0)
  return (
    <TerminalPane
      key={`shell:${name}:${session}`}
      source={api.containerShellPath(name)}
      interactive
      input="always"
      reconnect={false}
      emptyText=""
      waitingText={t('container.shellStarting')}
      ended={
        <button type="button" className="btn btn--primary" onClick={() => setSession((n) => n + 1)}>
          {t('container.newShell')}
        </button>
      }
    />
  )
}
