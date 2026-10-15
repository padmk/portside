import { useEffect } from 'react'
import { ConfigBanners } from '../features/config/ConfigBanners'
import { ConfigDialog } from '../features/config/ConfigDialog'
import { ServerDialog } from '../features/dialogs/ServerDialog'
import { Overview } from '../features/overview/Overview'
import { countsOf } from '../features/overview/status'
import { Palette } from '../features/palette/Palette'
import { PortsDialog } from '../features/ports/PortsDialog'
import { SettingsDialog } from '../features/settings/SettingsDialog'
import { ShortcutsDialog } from '../features/settings/ShortcutsDialog'
import { DetailPane } from '../features/servers/DetailPane'
import { DetailRail } from '../features/servers/DetailRail'
import { EmptyState } from '../features/servers/EmptyState'
import { useT } from '../i18n'
import { useLive } from '../state/live'
import { closeServer, useSelectedName } from '../state/route'
import { useUi } from '../state/ui'
import { Toasts } from '../ui/Toasts'
import { brand } from '../brand'
import { Mark } from './Mark'
import { Footer } from './Footer'
import { useShortcuts } from './shortcuts'
import { TopBar } from './TopBar'
import './layout.css'

export function App() {
  const t = useT()
  const { state, connection } = useLive()
  const { dialog } = useUi()
  const selectedName = useSelectedName()

  const servers = state?.servers ?? []
  const groups = state?.groups ?? []
  const selected = selectedName ? servers.find((s) => s.name === selectedName) : undefined

  useShortcuts(servers, groups, selected ? selected.name : null)

  // A link to a server that no longer exists falls back to the overview.
  useEffect(() => {
    if (state && selectedName && !selected) closeServer()
  }, [state, selectedName, selected])

  useEffect(() => {
    if (!state) {
      document.title = brand.name
      return
    }
    const c = countsOf(state.servers)
    const parts = [`● ${c.running}`]
    if (c.crashed) parts.push(`◆ ${c.crashed}`)
    document.title = `${parts.join(' ')} · ${brand.name}`
  }, [state])

  return (
    <div className="app">
      <TopBar servers={servers} connection={connection} inDetail={!!selected} />
      {state && <ConfigBanners config={state.config} />}
      <main className="main">
        {!state ? (
          <div className="boot" role="status">
            <Mark className="boot-mark" />
            {connection === 'unauthorized' ? (
              <>
                <h1 className="boot-title">{t('auth.title')}</h1>
                <p>{t('auth.body')}</p>
                <code className="mono">portside open</code>
              </>
            ) : (
              <p>{connection === 'connecting' ? t('top.connecting') : t('boot.unreachable')}</p>
            )}
          </div>
        ) : servers.length === 0 ? (
          <EmptyState />
        ) : selected ? (
          <div className="detail-layout">
            <DetailRail servers={servers} groups={groups} selected={selected.name} />
            <DetailPane key={selected.name} server={selected} servers={servers} />
          </div>
        ) : (
          <Overview servers={servers} groups={groups} />
        )}
      </main>
      {state && <Footer state={state} connection={connection} />}

      {dialog?.kind === 'server' && <ServerDialog key={dialog.edit ?? dialog.mode} mode={dialog.mode} edit={dialog.edit} />}
      {dialog?.kind === 'ports' && <PortsDialog />}
      {dialog?.kind === 'config' && <ConfigDialog />}
      {dialog?.kind === 'settings' && <SettingsDialog />}
      {dialog?.kind === 'shortcuts' && <ShortcutsDialog />}
      {dialog?.kind === 'palette' && state && <Palette state={state} />}
      <Toasts />
    </div>
  )
}
