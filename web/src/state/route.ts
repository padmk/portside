import { useSyncExternalStore } from 'react'

/** The open server is carried in the URL hash as `#/<name>`; no hash means the overview. */
function read(): string | null {
  const m = /^#\/([^/]+)(?:\/terminal)?$/.exec(location.hash)
  if (!m || !m[1]) return null
  try {
    return decodeURIComponent(m[1])
  } catch {
    return m[1]
  }
}

const serverHash = (name: string) => `#/${encodeURIComponent(name)}`

/** Whether the server's detail page is open. */
export const isSelected = (name: string) => location.hash === serverHash(name)

/** Opens a server's detail page, or the overview for `null`. Going from the overview to a server adds a history entry. */
export function selectServer(name: string | null) {
  const next = name ? serverHash(name) : ''
  if (location.hash === next) return
  const url = next || location.pathname + location.search
  if (name && !location.hash) history.pushState(null, '', url)
  else history.replaceState(null, '', url)
  window.dispatchEvent(new HashChangeEvent('hashchange'))
}

export const closeServer = () => selectServer(null)

/** `#/<name>/terminal` (used by the menu bar app's Show Logs) opens the server on its terminal. */
export function takeTerminalLink(): boolean {
  const m = /^#\/([^/]+)\/terminal$/.exec(location.hash)
  if (!m) return false
  history.replaceState(null, '', `#/${m[1]}`)
  return true
}

const AGENTS = '#settings/agents'

/** `#settings/agents` (used by the menu bar app's Connect Coding Agents) opens Settings on the coding agents. */
export function takeAgentsLink(): boolean {
  if (location.hash !== AGENTS) return false
  history.replaceState(null, '', location.pathname + location.search)
  return true
}

export function useSelectedName(): string | null {
  return useSyncExternalStore(
    (cb) => (window.addEventListener('hashchange', cb), () => window.removeEventListener('hashchange', cb)),
    read,
  )
}

const CONTAINERS = '#containers'

/** Opens the containers page. */
export function openContainers() {
  if (location.hash === CONTAINERS) return
  history.pushState(null, '', CONTAINERS)
  window.dispatchEvent(new HashChangeEvent('hashchange'))
}

/** Whether the containers page is open. */
export const onContainersPage = () => location.hash === CONTAINERS

export function useContainersPage(): boolean {
  return useSyncExternalStore(
    (cb) => (window.addEventListener('hashchange', cb), () => window.removeEventListener('hashchange', cb)),
    onContainersPage,
  )
}
