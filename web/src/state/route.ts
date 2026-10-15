import { useSyncExternalStore } from 'react'

/** The open server is carried in the URL hash as `#/<name>`; no hash means the overview. */
function read(): string | null {
  const m = /^#\/(.+)$/.exec(location.hash)
  if (!m || !m[1]) return null
  try {
    return decodeURIComponent(m[1])
  } catch {
    return m[1]
  }
}

/** Opens a server's detail page, or the overview for `null`. Going from the overview to a server adds a history entry. */
export function selectServer(name: string | null) {
  const next = name ? `#/${encodeURIComponent(name)}` : ''
  if (location.hash === next) return
  const url = next || location.pathname + location.search
  if (name && !location.hash) history.pushState(null, '', url)
  else history.replaceState(null, '', url)
  window.dispatchEvent(new HashChangeEvent('hashchange'))
}

export const closeServer = () => selectServer(null)

export function useSelectedName(): string | null {
  return useSyncExternalStore(
    (cb) => (window.addEventListener('hashchange', cb), () => window.removeEventListener('hashchange', cb)),
    read,
  )
}
