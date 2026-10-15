import { useSyncExternalStore } from 'react'
import { api, ApiError } from '../../api/client'
import type { BulkAction, OpenTarget, ServerView } from '../../api/types'
import { applyState } from '../../state/live'
import { selectServer } from '../../state/route'
import { toast, toastError } from '../../state/toasts'
import { t } from '../../i18n'
import { isAlive } from './format'

const busy = new Set<string>()
let version = 0
const listeners = new Set<() => void>()
const emit = () => {
  version++
  listeners.forEach((l) => l())
}

export function useBusy(key: string): boolean {
  useSyncExternalStore(
    (cb) => (listeners.add(cb), () => listeners.delete(cb)),
    () => version,
  )
  return busy.has(key)
}

/** Runs one request per key at a time and reports failures as toasts. */
export async function run<T>(key: string, fn: () => Promise<T>): Promise<T | undefined> {
  if (busy.has(key)) return undefined
  busy.add(key)
  emit()
  try {
    return await fn()
  } catch (e) {
    toastError(e)
    if (e instanceof ApiError && e.code === 'port_busy') selectServer(key)
    return undefined
  } finally {
    busy.delete(key)
    emit()
  }
}

export const startServer = (name: string, takeover = false) => run(name, () => api.start(name, takeover))
export const stopServer = (name: string) => run(name, () => api.stop(name))
export const restartServer = (name: string, takeover = false) => run(name, () => api.restart(name, takeover))

export function toggleServer(v: ServerView) {
  if (v.status === 'stopping') return
  return isAlive(v) ? stopServer(v.name) : startServer(v.name)
}

export async function bulk(key: string, action: BulkAction, target: { names: string[] } | { all: true }) {
  const res = await run(`bulk:${key}`, () => api.bulk(action, target))
  const failed = res?.results.filter((r) => r.error) ?? []
  for (const f of failed) toast(`${f.name}: ${f.error}`, 'error')
}

export const setEnabled = (name: string, enabled: boolean) =>
  run(name, async () => applyState(await api.setEnabled(name, enabled)))

export const removeServer = (name: string) =>
  run(name, async () => {
    applyState(await api.deleteServer(name))
    if (location.hash === `#/${encodeURIComponent(name)}`) selectServer(null)
  })

export const openOnDesktop = (target: OpenTarget, name?: string) =>
  run(`open:${target}:${name ?? ''}`, async () => {
    await api.open(target, name)
  })

export const stopDaemon = () =>
  run('daemon', async () => {
    await api.shutdown()
    toast(t('menu.daemonStopped'))
  })
