import { useEffect, useState } from 'react'
import { api } from '../../api/client'
import type { ListenerView } from '../../api/types'

let cache: { at: number; promise: Promise<ListenerView[]> } | null = null

/** One listeners request is shared by every card that asks within a few seconds. */
function listeners(): Promise<ListenerView[]> {
  if (cache && Date.now() - cache.at < 4000) return cache.promise
  const promise = api.listeners().then((r) => r.listeners)
  const entry = { at: Date.now(), promise }
  cache = entry
  promise.catch(() => {
    if (cache === entry) cache = null
  })
  return promise
}

/**
 * Why the process holding a port cannot be taken over: a reason string when it is
 * protected, null when it can be stopped, undefined while the lookup runs.
 */
export function useProtectedReason(pid: number | undefined, port: number): string | null | undefined {
  const [reason, setReason] = useState<string | null | undefined>(undefined)
  useEffect(() => {
    if (!pid) return
    let live = true
    setReason(undefined)
    listeners()
      .then((rows) => live && setReason(rows.find((l) => l.pid === pid)?.protected ?? null))
      .catch(() => live && setReason(null))
    return () => {
      live = false
    }
  }, [pid, port])
  return reason
}
