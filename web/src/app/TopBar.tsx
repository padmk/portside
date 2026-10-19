import { Container, FileCog, Keyboard, LayoutGrid, List, Menu as MenuIcon, Network, Play, Plus, Power, Search, Settings, Square } from 'lucide-react'
import { useState } from 'react'
import type { DockerState, RemoteConnection, ServerView } from '../api/types'
import { useT } from '../i18n'
import { closeServer, openContainers } from '../state/route'
import { openAddServer, openDialog } from '../state/ui'
import { Lamp } from '../ui/Lamp'
import { modCombo, paletteKey } from '../ui/keys'
import { Menu, MenuItem, MenuSeparator } from '../ui/Menu'
import { ConfirmDialog } from '../features/dialogs/ConfirmDialog'
import { countsOf } from '../features/overview/status'
import { STATUS_GROUPS, type StatusGroupId } from '../features/overview/status'
import { layoutFor, setFilter, setLayout, toggleStatusFilter, useOverviewState } from '../features/overview/overviewState'
import { bulk, stopAll, stopDaemon } from '../features/servers/actions'
import { hasProcess } from '../features/servers/format'
import { containerIssues, containersOf, usesDocker } from '../features/containers/status'
import type { Connection } from '../state/live'
import { ComputersButton, RemotePill } from '../features/remote/RemotePill'
import { brand } from '../brand'
import { filterInputId } from './shortcuts'
import { Mark } from './Mark'

interface Props {
  servers: ServerView[]
  docker?: DockerState
  connection: Connection
  /** The other computer shown, or null for this one. */
  remote: RemoteConnection | null
  /** A server's detail page is open: the filter and layout toggle belong to the overview. */
  inDetail: boolean
}


export function TopBar({ servers, docker, connection, remote, inDetail }: Props) {
  const t = useT()
  const { filter, status, layoutChoice } = useOverviewState()
  const [confirmStop, setConfirmStop] = useState(false)
  const counts = countsOf(servers)
  const layout = layoutFor(layoutChoice, servers.length)
  // Without an engine and without servers that need one, containers are not part of the picture.
  const showContainers = docker?.status === 'ready' || servers.some(usesDocker)
  // An engine that is off matters only to a server that needs it and is meant to run.
  const containerTrouble =
    docker?.status === 'unavailable'
      ? servers.some((v) => usesDocker(v) && (v.wanted || hasProcess(v)))
      : servers.some((v) => containerIssues(v, containersOf(docker, v.name)).length > 0)

  const pickStatus = (id: StatusGroupId) => {
    toggleStatusFilter(id)
    if (inDetail) closeServer()
  }

  return (
    <header className="topbar">
      <a className="brand" href="#" aria-label={brand.name} onClick={(e) => { e.preventDefault(); closeServer() }}>
        <Mark />
        <span className="wordmark">{brand.name}</span>
      </a>
      {remote && <RemotePill remote={remote} />}

      <ul className="counts" aria-label={t('top.counts')}>
        {STATUS_GROUPS.filter((g) => g.id === 'running' || counts[g.id] > 0 || status === g.id).map((g) => (
          <li key={g.id}>
            <button
              type="button"
              className={`count status-text--${g.lamp}`}
              aria-pressed={status === g.id}
              aria-label={t('top.filterBy', { status: t(`status.${g.id}`) })}
              title={t('top.filterBy', { status: t(`status.${g.id}`) })}
              onClick={() => pickStatus(g.id)}
            >
              <Lamp status={g.lamp} decorative />
              <span className="num count-num">{counts[g.id]}</span>
              <span className="count-label">{t(`count.${g.id}`, { n: counts[g.id] })}</span>
            </button>
          </li>
        ))}
      </ul>
      {connection !== 'open' && connection !== 'unauthorized' && (
        <span className="conn" role="status">
          <Lamp status="starting" decorative />
          {t(connection === 'connecting' ? 'top.connecting' : 'top.reconnecting')}
        </span>
      )}

      <span className="spacer" />

      {!inDetail && (
        <div className="search-input top-filter">
          <Search size={17} aria-hidden="true" />
          <input
            id={filterInputId}
            className="input"
            type="search"
            value={filter}
            placeholder={t('top.filter')}
            aria-label={t('top.filter')}
            onChange={(e) => setFilter(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
                setFilter('')
                e.currentTarget.blur()
              }
              if (e.key === 'Enter' || e.key === 'ArrowDown') {
                e.preventDefault()
                e.currentTarget.blur()
              }
            }}
          />
          <kbd className="filter-key" aria-hidden="true">/</kbd>
        </div>
      )}
      <button type="button" className="btn palette-btn" onClick={() => openDialog({ kind: 'palette' })} aria-label={t('top.search')} title={t('top.search')}>
        <span aria-hidden="true" className="palette-keys">{modCombo(paletteKey, t)}</span>
      </button>
      <button type="button" className="btn top-add" onClick={() => openAddServer()}>
        <Plus size={17} aria-hidden="true" />
        {t('list.add')}
      </button>
      {!inDetail && (
        <div className="seg" role="group" aria-label={t('view.label')}>
          <button type="button" className="seg-btn" aria-pressed={layout === 'cards'} aria-label={t('view.cards')} title={t('view.cards')} onClick={() => setLayout('cards')}>
            <LayoutGrid size={17} aria-hidden="true" />
          </button>
          <button type="button" className="seg-btn" aria-pressed={layout === 'list'} aria-label={t('view.list')} title={t('view.list')} onClick={() => setLayout('list')}>
            <List size={17} aria-hidden="true" />
          </button>
        </div>
      )}
      {!remote && <ComputersButton />}
      {showContainers && (
        <button type="button" className="icon-btn icon-btn--boxed top-containers" aria-label={t('containers.title')} title={t('containers.title')} onClick={openContainers}>
          <Container size={18} />
          {containerTrouble && <span className="top-dot" aria-hidden="true" />}
        </button>
      )}
      <Menu label={t('top.menu')} trigger={<MenuIcon size={19} />} className="icon-btn icon-btn--boxed">
        <MenuItem icon={<Plus size={16} />} onSelect={() => openAddServer()}>{t('menu.addServer')}</MenuItem>
        <MenuItem icon={<Play size={16} />} onSelect={() => void bulk('all', 'start', { all: true })}>{t('menu.startAll')}</MenuItem>
        <MenuItem icon={<Square size={15} />} onSelect={() => stopAll(servers)}>{t('menu.stopAll')}</MenuItem>
        <MenuSeparator />
        <MenuItem icon={<Network size={16} />} onSelect={() => openDialog({ kind: 'ports' })}>{t('menu.ports')}</MenuItem>
        <MenuItem icon={<Container size={16} />} onSelect={openContainers}>{t('containers.title')}</MenuItem>
        <MenuItem icon={<FileCog size={16} />} onSelect={() => openDialog({ kind: 'config' })}>{t('menu.editConfig')}</MenuItem>
        <MenuSeparator />
        <MenuItem icon={<Settings size={16} />} onSelect={() => openDialog({ kind: 'settings' })}>{t('menu.settings')}</MenuItem>
        <MenuItem icon={<Keyboard size={16} />} onSelect={() => openDialog({ kind: 'shortcuts' })}>{t('menu.shortcuts')}</MenuItem>
        {!remote && (
          <>
            <MenuSeparator />
            <MenuItem icon={<Power size={16} />} danger onSelect={() => setConfirmStop(true)}>{t('menu.stopDaemon')}</MenuItem>
          </>
        )}
      </Menu>
      {confirmStop && (
        <ConfirmDialog
          title={t('confirm.daemonTitle')}
          message={t('confirm.daemonBody')}
          confirmLabel={t('menu.stopDaemon')}
          onConfirm={() => stopDaemon()}
          onClose={() => setConfirmStop(false)}
        />
      )}
    </header>
  )
}
