import type { ServerView } from '../../api/types'
import { storageKey } from '../../brand'
import { write } from '../../state/prefs'
import { createStore, useStore } from '../../state/store'
import { groupOf, type StatusGroupId } from './status'

export type Layout = 'cards' | 'list'

/** Above this many servers the overview starts in list mode. */
export const CARDS_MAX = 12

const COLLAPSED_KEY = storageKey('collapsed')
const LAYOUT_KEY = storageKey('layout')

function loadCollapsed(): Set<string> {
  try {
    return new Set(JSON.parse(localStorage.getItem(COLLAPSED_KEY) ?? '[]') as string[])
  } catch {
    return new Set()
  }
}

function loadLayout(): Layout | null {
  try {
    const v = localStorage.getItem(LAYOUT_KEY)
    return v === 'cards' || v === 'list' ? v : null
  } catch {
    return null
  }
}

interface OverviewState {
  filter: string
  status: StatusGroupId | null
  collapsed: Set<string>
  /** The user's explicit choice; null follows the server count. */
  layoutChoice: Layout | null
  /** The server the keyboard acts on in the overview. */
  cursor: string | null
}

const store = createStore<OverviewState>({ filter: '', status: null, collapsed: loadCollapsed(), layoutChoice: loadLayout(), cursor: null })

function publish(next: Partial<OverviewState>) {
  store.set({ ...store.get(), ...next })
}

export const setFilter = (filter: string) => publish({ filter })
export const setCursor = (cursor: string | null) => publish({ cursor })

/** Clicking the active status again clears it. */
export const toggleStatusFilter = (status: StatusGroupId) => publish({ status: store.get().status === status ? null : status })
export const clearFilters = () => publish({ filter: '', status: null })

export function setLayout(layout: Layout) {
  write(LAYOUT_KEY, layout)
  publish({ layoutChoice: layout })
}

export function toggleGroup(key: string) {
  const next = new Set(store.get().collapsed)
  if (!next.delete(key)) next.add(key)
  write(COLLAPSED_KEY, JSON.stringify([...next]))
  publish({ collapsed: next })
}

export function useOverviewState(): OverviewState {
  return useStore(store)
}

export const layoutFor = (choice: Layout | null, count: number): Layout => choice ?? (count <= CARDS_MAX ? 'cards' : 'list')

/** How much room each card gets, from the number of servers. */
export type Density = 'solo' | 'roomy' | 'cozy' | 'compact'

export function densityFor(count: number): Density {
  if (count <= 1) return 'solo'
  if (count <= 4) return 'roomy'
  if (count <= CARDS_MAX) return 'cozy'
  return 'compact'
}

export interface Section {
  /** Group name, or null for servers without a group. */
  group: string | null
  /** Every server of the group, for counts and group actions. */
  all: ServerView[]
  /** The servers that pass the current filters. */
  shown: ServerView[]
}

const matches = (v: ServerView, q: string) =>
  [v.name, v.group ?? '', String(v.port), v.cmd, v.dirDisplay].some((s) => s.toLowerCase().includes(q))

export function buildSections(servers: ServerView[], groups: string[], filter: string, status: StatusGroupId | null): Section[] {
  const q = filter.trim().toLowerCase()
  const pass = (v: ServerView) => (!q || matches(v, q)) && (!status || groupOf(v) === status)
  const order = [...groups]
  for (const v of servers) if (v.group && !order.includes(v.group)) order.push(v.group)
  const sections: Section[] = order.map((g) => {
    const all = servers.filter((v) => v.group === g)
    return { group: g, all, shown: all.filter(pass) }
  })
  const rest = servers.filter((v) => !v.group)
  if (rest.length > 0) sections.push({ group: null, all: rest, shown: rest.filter(pass) })
  return sections.filter((s) => s.shown.length > 0)
}

/** Servers in the order they appear on screen, skipping collapsed groups. */
export function visibleServers(sections: Section[], collapsed: Set<string>): ServerView[] {
  return sections.flatMap((s) => (collapsed.has(s.group ?? '') ? [] : s.shown))
}
