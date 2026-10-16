import { useEffect } from 'react'
import { Unplug } from 'lucide-react'
import { disconnectRemote } from '../features/remote/actions'
import { ConfigBanners } from '../features/config/ConfigBanners'
import { ConfigDialog } from '../features/config/ConfigDialog'
import { ContainersPage } from '../features/containers/ContainersPage'
import { ServerDialog } from '../features/dialogs/ServerDialog'
import { Overview } from '../features/overview/Overview'
import { countsOf } from '../features/overview/status'
import { Palette } from '../features/palette/Palette'
import { PortsDialog } from '../features/ports/PortsDialog'
import { ComputersDialog } from '../features/remote/ComputersDialog'
import { SettingsDialog } from '../features/settings/SettingsDialog'
import { ShortcutsDialog } from '../features/settings/ShortcutsDialog'
import { DetailPane } from '../features/servers/DetailPane'
import { DetailRail } from '../features/servers/DetailRail'
import { EmptyState } from '../features/servers/EmptyState'
import { useT } from '../i18n'
import { useLive } from '../state/live'
import { closeServer, takeTerminalLink, useContainersPage, useSelectedName } from '../state/route'
import { closeDialog, getUi, showTerminal, useUi } from '../state/ui'
import { Toasts } from '../ui/Toasts'
import { brand } from '../brand'
import { Mark } from './Mark'
import { Footer } from './Footer'
import { useShortcuts } from './shortcuts'
import { TopBar } from './TopBar'
import './layout.css'

export function App() {
  const t = useT()
  const { state, connection, remote } = useLive()
  const { dialog } = useUi()
  const selectedName = useSelectedName()
  const containersPage = useContainersPage()

  const servers = state?.servers ?? []
  const groups = state?.groups ?? []
  const selected = selectedName ? servers.find((s) => s.name === selectedName) : undefined

  useShortcuts(servers, groups, selected ? selected.name : null)

  useEffect(() => {
    const check = () => {
      if (takeTerminalLink()) showTerminal()
    }
    check()
    window.addEventListener('hashchange', check)
    return () => window.removeEventListener('hashchange', check)
  }, [])

  // A link to a server that no longer exists falls back to the overview.
  useEffect(() => {
    if (state && selectedName && !selected) closeServer()
  }, [state, selectedName, selected])

  // Dialogs hold data of the computer they were opened for; switching computers closes them.
  const machine = remote?.peer.dnsName ?? ''
  useEffect(() => {
    const open = getUi().dialog
    if (open && open.kind !== 'computers') closeDialog()
  }, [machine])

  useEffect(() => {
    const prefix = remote ? `${remote.peer.name} · ` : ''
    if (!state) {
      document.title = `${prefix}${brand.name}`
      return
    }
    const c = countsOf(state.servers)
    const parts = [`● ${c.running}`]
    if (c.crashed) parts.push(`◆ ${c.crashed}`)
    document.title = `${prefix}${parts.join(' ')} · ${brand.name}`
  }, [state, remote])

  return (
    <div className={remote ? 'app app--remote' : 'app'}>
      <TopBar servers={servers} docker={state?.docker} connection={connection} remote={remote} inDetail={!!selected || containersPage} />
      {state && <ConfigBanners config={state.config} />}
      <main className="main" key={machine}>
        {connection === 'unauthorized' ? (
          <div className="boot" role="status">
            <Mark className="boot-mark" />
            <h1 className="boot-title">{t('auth.title')}</h1>
            <p>{t('auth.body')}</p>
            <code className="mono">portside open</code>
          </div>
        ) : !state && remote ? (
          <div className="boot boot--remote" role="status">
            <Mark className="boot-mark" />
            <h1 className="boot-title">{t(remote.status === 'reconnecting' ? 'remote.unreachable' : 'remote.connecting', { name: remote.peer.name })}</h1>
            {remote.error && <p>{remote.error}</p>}
            <button type="button" className="btn" onClick={() => void disconnectRemote()}>
              <Unplug size={15} aria-hidden="true" />
              {t('remote.disconnect')}
            </button>
          </div>
        ) : !state ? (
          <div className="boot" role="status">
            <Mark className="boot-mark" />
            <p>{connection === 'connecting' ? t('top.connecting') : t('boot.unreachable')}</p>
          </div>
        ) : containersPage ? (
          <ContainersPage docker={state.docker} />
        ) : servers.length === 0 ? (
          <EmptyState />
        ) : selected ? (
          <div className="detail-layout">
            <DetailRail servers={servers} groups={groups} selected={selected.name} />
            <DetailPane key={selected.name} server={selected} servers={servers} docker={state.docker} />
          </div>
        ) : (
          <Overview servers={servers} groups={groups} />
        )}
      </main>
      {state && <Footer state={state} connection={connection} remote={remote} />}

      {dialog?.kind === 'server' && <ServerDialog key={dialog.edit ?? dialog.mode} mode={dialog.mode} edit={dialog.edit} />}
      {dialog?.kind === 'ports' && <PortsDialog />}
      {dialog?.kind === 'config' && <ConfigDialog />}
      {dialog?.kind === 'settings' && <SettingsDialog />}
      {dialog?.kind === 'shortcuts' && <ShortcutsDialog />}
      {dialog?.kind === 'computers' && <ComputersDialog />}
      {dialog?.kind === 'palette' && state && <Palette state={state} />}
      <Toasts />
    </div>
  )
}
