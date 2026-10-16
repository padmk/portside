import { useEffect, useRef } from 'react'
import type { ServerView } from '../api/types'
import type { Key } from '../i18n'
import { closeServer, openContainers, selectServer } from '../state/route'
import { closeDialog, openAddServer, openDialog, showTerminal } from '../state/ui'
import { restartServer, toggleServer } from '../features/servers/actions'
import { hasProcess } from '../features/servers/format'
import {
  buildSections,
  clearFilters,
  setCursor,
  setLayout,
  useOverviewState,
  visibleServers,
} from '../features/overview/overviewState'

/** The shortcut sheet and the key handler below describe the same set of keys. */
export interface ShortcutGroup {
  title: Key
  items: { keys: string[]; label: Key; chord?: boolean }[]
}

export const SHORTCUTS: ShortcutGroup[] = [
  {
    title: 'shortcuts.nav',
    items: [
      { keys: ['j'], label: 'shortcuts.next' },
      { keys: ['k'], label: 'shortcuts.prev' },
      { keys: ['Enter'], label: 'shortcuts.openDetail' },
      { keys: ['Esc'], label: 'shortcuts.back' },
      { keys: ['/'], label: 'shortcuts.filter' },
    ],
  },
  {
    title: 'shortcuts.server',
    items: [
      { keys: ['s'], label: 'shortcuts.startStop' },
      { keys: ['r'], label: 'shortcuts.restart' },
      { keys: ['o'], label: 'shortcuts.openUrl' },
    ],
  },
  {
    title: 'shortcuts.global',
    items: [
      { keys: ['mod', 'K'], label: 'shortcuts.palette' },
      { keys: ['c'], label: 'shortcuts.cards' },
      { keys: ['l'], label: 'shortcuts.list' },
      { keys: ['g', 'p'], label: 'shortcuts.ports', chord: true },
      { keys: ['g', 'd'], label: 'shortcuts.containers', chord: true },
      { keys: ['g', 'c'], label: 'shortcuts.config', chord: true },
      { keys: ['g', 's'], label: 'shortcuts.settings', chord: true },
      { keys: ['g', 'a'], label: 'shortcuts.add', chord: true },
      { keys: ['?'], label: 'shortcuts.help' },
    ],
  },
]

export const isTyping = (el: EventTarget | null) => {
  if (!(el instanceof HTMLElement)) return false
  return el.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName) || el.classList.contains('xterm-helper-textarea')
}

const FILTER_ID = 'overview-filter'

export function focusFilter() {
  // In a detail page the filter lives on the overview, so go there first.
  closeServer()
  requestAnimationFrame(() => {
    const input = document.getElementById(FILTER_ID) as HTMLInputElement | null
    input?.focus()
    input?.select()
  })
}

export const filterInputId = FILTER_ID

/** Global keyboard handling: the palette, navigation, the selected server's actions and `g` chords. */
export function useShortcuts(servers: ServerView[], groups: string[], selectedName: string | null) {
  const { filter, status, collapsed, cursor } = useOverviewState()
  const latest = useRef({ servers, groups, selectedName, filter, status, collapsed, cursor })
  latest.current = { servers, groups, selectedName, filter, status, collapsed, cursor }

  useEffect(() => {
    let chord = 0
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        if (document.querySelector('dialog[open].palette')) closeDialog()
        else if (!document.querySelector('dialog[open]')) openDialog({ kind: 'palette' })
        return
      }
      if (e.metaKey || e.ctrlKey || e.altKey || e.defaultPrevented) return
      if (isTyping(e.target) || document.querySelector('dialog[open]')) return
      if (e.target instanceof HTMLElement && e.target.closest('[role="menu"]')) return

      const { servers, groups, selectedName, filter, status, collapsed, cursor } = latest.current
      const inDetail = selectedName !== null && servers.some((s) => s.name === selectedName)
      const filtering = filter.trim() !== '' || status !== null
      const order = inDetail
        ? visibleServers(buildSections(servers, groups, '', null), new Set())
        : visibleServers(buildSections(servers, groups, filter, status), filtering ? new Set() : collapsed)
      const current = servers.find((s) => s.name === (inDetail ? selectedName : cursor))

      if (chord && Date.now() - chord < 1200) {
        chord = 0
        const dialogs: Record<string, () => void> = {
          p: () => openDialog({ kind: 'ports' }),
          d: openContainers,
          c: () => openDialog({ kind: 'config' }),
          s: () => openDialog({ kind: 'settings' }),
          a: () => openAddServer(),
        }
        const run = dialogs[e.key]
        if (run) {
          e.preventDefault()
          run()
          return
        }
      }
      chord = 0

      const step = (delta: number) => {
        if (order.length === 0) return
        const idx = current ? order.findIndex((s) => s.name === current.name) : -1
        const next = order[Math.max(0, Math.min(order.length - 1, idx < 0 ? (delta > 0 ? 0 : order.length - 1) : idx + delta))]
        if (!next) return
        if (inDetail) selectServer(next.name)
        else setCursor(next.name)
      }

      switch (e.key) {
        case 'g':
          chord = Date.now()
          break
        case '?':
          e.preventDefault()
          openDialog({ kind: 'shortcuts' })
          break
        case '/':
          e.preventDefault()
          focusFilter()
          break
        case 'c':
          setLayout('cards')
          break
        case 'l':
          setLayout('list')
          break
        case 'ArrowDown':
        case 'ArrowRight':
        case 'j':
          e.preventDefault()
          step(1)
          break
        case 'ArrowUp':
        case 'ArrowLeft':
        case 'k':
          e.preventDefault()
          step(-1)
          break
        case 'Enter':
          if (e.target instanceof HTMLElement && e.target.closest('button,a,summary')) break
          if (inDetail) showTerminal()
          else if (current) selectServer(current.name)
          else break
          e.preventDefault()
          break
        case 'Escape':
          if (location.hash === '#containers') closeServer()
          else if (inDetail) {
            setCursor(selectedName)
            closeServer()
          } else if (filtering) clearFilters()
          else setCursor(null)
          break
        case 's':
          if (current) toggleServer(current)
          break
        case 'r':
          // Also brings back a crashed or exited server, like its Restart button.
          if (current && current.status !== 'stopping' && (hasProcess(current) || current.status === 'crashed' || current.status === 'exited')) {
            restartServer(current.name)
          }
          break
        case 'o':
          if (current?.url && (current.status === 'running' || current.status === 'starting')) window.open(current.url, '_blank', 'noreferrer')
          break
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
}
