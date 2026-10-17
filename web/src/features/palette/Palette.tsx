import { useMemo, useRef, useState, type ReactNode } from 'react'
import {
  ExternalLink,
  FileCog,
  Keyboard,
  Languages,
  LayoutGrid,
  List,
  Moon,
  Palette as PaletteIcon,
  Play,
  Plus,
  RotateCw,
  Settings,
  Square,
  SquareTerminal,
  Network,
  Container,
  MonitorSmartphone,
  Unplug,
} from 'lucide-react'
import type { BulkAction, State } from '../../api/types'
import { useT, type Key, type Translate } from '../../i18n'
import { setLocale, setThemeFamily, toggleThemeMode, usePrefs } from '../../state/prefs'
import { FAMILIES } from '../../theme/registry'
import { buildSections, setLayout } from '../overview/overviewState'
import { openContainers } from '../../state/route'
import { closeDialog, openAddServer, openDialog } from '../../state/ui'
import { Dialog } from '../../ui/Dialog'
import { bulk, groupKey, groupTargets, restartServer, startServer, stopAll, stopServer } from '../servers/actions'
import { viewLogs } from '../servers/StateActions'
import { disconnectRemote } from '../remote/actions'
import { canStop, hasProcess } from '../servers/format'
import { fuzzy } from './fuzzy'
import './palette.css'

interface Command {
  id: string
  label: string
  hint?: string
  icon: ReactNode
  run: () => void
  /** Acts on several servers; ranked below the commands for one server that match too. */
  bulk?: boolean
}

function buildCommands(state: State, t: Translate, locale: string): Command[] {
  const cmds: Command[] = [
    { id: 'add', label: t('palette.addServer'), icon: <Plus size={15} />, run: () => openAddServer() },
    { id: 'startAll', label: t('menu.startAll'), icon: <Play size={15} />, bulk: true, run: () => void bulk('all', 'start', { all: true }) },
    { id: 'stopAll', label: t('menu.stopAll'), icon: <Square size={14} />, bulk: true, run: () => stopAll(state.servers) },
    { id: 'ports', label: t('menu.ports'), icon: <Network size={15} />, run: () => openDialog({ kind: 'ports' }) },
    { id: 'containers', label: t('containers.title'), icon: <Container size={15} />, run: openContainers },
    { id: 'computers', label: t('remote.open'), icon: <MonitorSmartphone size={15} />, run: () => openDialog({ kind: 'computers' }) },
    ...(state.remote
      ? [{ id: 'disconnect', label: t('remote.disconnectFrom', { name: state.remote.peer.name }), icon: <Unplug size={15} />, run: () => void disconnectRemote() }]
      : []),
    { id: 'config', label: t('menu.editConfig'), icon: <FileCog size={15} />, run: () => openDialog({ kind: 'config' }) },
    { id: 'settings', label: t('menu.settings'), icon: <Settings size={15} />, run: () => openDialog({ kind: 'settings' }) },
    { id: 'shortcuts', label: t('menu.shortcuts'), icon: <Keyboard size={15} />, run: () => openDialog({ kind: 'shortcuts' }) },
    { id: 'cards', label: t('palette.layoutCards'), icon: <LayoutGrid size={15} />, run: () => setLayout('cards') },
    { id: 'list', label: t('palette.layoutList'), icon: <List size={15} />, run: () => setLayout('list') },
    { id: 'mode', label: t('palette.toggleTheme'), icon: <Moon size={15} />, run: toggleThemeMode },
    ...FAMILIES.map((f) => ({
      id: `family:${f.id}`,
      label: t('palette.theme', { name: f.id === 'neutral' ? t('theme.neutral') : f.label }),
      icon: <PaletteIcon size={15} />,
      run: () => setThemeFamily(f.id),
    })),
    { id: 'lang', label: t('palette.switchLanguage'), icon: <Languages size={15} />, run: () => setLocale(locale === 'pl' ? 'en' : 'pl') },
  ]
  for (const v of state.servers) {
    const hint = v.group ?? ''
    if (canStop(v)) {
      cmds.push({ id: `stop:${v.name}`, label: t('palette.stop', { name: v.name }), hint, icon: <Square size={14} />, run: () => void stopServer(v.name) })
      cmds.push({ id: `restart:${v.name}`, label: t('palette.restart', { name: v.name }), hint, icon: <RotateCw size={14} />, run: () => void restartServer(v.name) })
    } else if (!hasProcess(v)) {
      cmds.push({ id: `start:${v.name}`, label: t('palette.start', { name: v.name }), hint, icon: <Play size={15} />, run: () => void startServer(v.name) })
    }
    cmds.push({ id: `open:${v.name}`, label: t('palette.open', { name: v.name }), hint, icon: <SquareTerminal size={15} />, run: () => viewLogs(v.name) })
    if (v.url && (v.status === 'running' || v.status === 'starting')) {
      cmds.push({ id: `browse:${v.name}`, label: t('palette.browse', { name: v.name }), hint: v.alias ? v.alias.replace(/^https?:\/\//, '') : '', icon: <ExternalLink size={14} />, run: () => window.open(v.url, '_blank', 'noreferrer') })
    }
  }
  for (const s of buildSections(state.servers, state.groups, '', null)) {
    if (s.group === null) continue
    const name = s.group
    const targets = groupTargets(s.all)
    const group = (action: BulkAction, label: Key, icon: ReactNode) => {
      if (targets[action].length === 0) return
      cmds.push({ id: `${action}Group:${name}`, label: t(label, { name }), icon, bulk: true, run: () => void bulk(groupKey(name), action, { names: targets[action] }) })
    }
    group('start', 'palette.startGroup', <Play size={15} />)
    group('restart', 'palette.restartGroup', <RotateCw size={14} />)
    group('stop', 'palette.stopGroup', <Square size={14} />)
  }
  return cmds
}

function Highlighted({ text, indices }: { text: string; indices: number[] }) {
  if (indices.length === 0) return <>{text}</>
  const set = new Set(indices)
  return (
    <>
      {Array.from(text).map((ch, i) => (set.has(i) ? <mark key={i}>{ch}</mark> : ch))}
    </>
  )
}

export function Palette({ state }: { state: State }) {
  const t = useT()
  const { locale } = usePrefs()
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const list = useRef<HTMLUListElement>(null)

  const all = useMemo(() => buildCommands(state, t, locale), [state, t, locale])
  const results = useMemo(() => {
    if (!query.trim()) return all.map((c) => ({ c, indices: [] as number[] }))
    return all
      .map((c) => ({ c, m: fuzzy(query, c.label) }))
      .filter((x): x is { c: Command; m: NonNullable<typeof x.m> } => x.m !== null)
      .sort((a, b) => Number(!!a.c.bulk) - Number(!!b.c.bulk) || b.m.score - a.m.score)
      .map((x) => ({ c: x.c, indices: x.m.indices }))
  }, [all, query])

  const run = (c: Command | undefined) => {
    if (!c) return
    closeDialog()
    c.run()
  }

  const move = (to: number) => {
    const i = Math.max(0, Math.min(results.length - 1, to))
    setActive(i)
    list.current?.children[i]?.scrollIntoView({ block: 'nearest' })
  }

  return (
    <Dialog title={t('palette.title')} bare size="sm" className="palette" onClose={closeDialog}>
      <div className="palette-input">
        <input
          data-autofocus
          className="palette-field"
          role="combobox"
          aria-expanded="true"
          aria-controls="palette-list"
          aria-activedescendant={results[active] ? `pal-${results[active].c.id}` : undefined}
          aria-label={t('palette.title')}
          placeholder={t('palette.placeholder')}
          value={query}
          spellCheck={false}
          autoComplete="off"
          onChange={(e) => {
            setQuery(e.target.value)
            setActive(0)
          }}
          onKeyDown={(e) => {
            if (e.key === 'ArrowDown' || (e.ctrlKey && e.key === 'n')) {
              e.preventDefault()
              move(active + 1)
            } else if (e.key === 'ArrowUp' || (e.ctrlKey && e.key === 'p')) {
              e.preventDefault()
              move(active - 1)
            } else if (e.key === 'Enter') {
              e.preventDefault()
              run(results[active]?.c)
            }
          }}
        />
        <kbd>esc</kbd>
      </div>
      <ul className="palette-list" id="palette-list" role="listbox" ref={list}>
        {results.length === 0 && <li className="palette-none">{t('palette.none')}</li>}
        {results.map(({ c, indices }, i) => (
          <li
            key={c.id}
            id={`pal-${c.id}`}
            role="option"
            aria-selected={i === active}
            className="palette-item"
            onMouseMove={() => i !== active && setActive(i)}
            onClick={() => run(c)}
          >
            <span className="ic" aria-hidden="true">{c.icon}</span>
            <span className="lbl"><Highlighted text={c.label} indices={indices} /></span>
            {c.hint && <span className="hint">{c.hint}</span>}
          </li>
        ))}
      </ul>
    </Dialog>
  )
}
