import { useSyncExternalStore } from 'react'
import { api, ApiError, isCurrent } from '../../api/client'
import type { BulkAction, OpenTarget, ServerView, State } from '../../api/types'
import { applyState, currentState } from '../../state/live'
import { selectServer } from '../../state/route'
import { toast, toastError } from '../../state/toasts'
import { openDialog, resetTerminal } from '../../state/ui'
import { t } from '../../i18n'
import { canStop, hasProcess } from './format'

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
  return canStop(v) ? stopServer(v.name) : startServer(v.name)
}

export async function bulk(key: string, action: BulkAction, target: { names: string[] } | { all: true }) {
  const res = await run(`bulk:${key}`, () => api.bulk(action, target))
  const failed = res?.results.filter((r) => r.error) ?? []
  for (const f of failed) toast(`${f.name}: ${f.error}`, 'error')
}

/** The busy key of a group's bulk actions; servers without a group share one. */
export const groupKey = (group: string | null | undefined) => group || '\0'

/** The members of a group each bulk action applies to; the daemon orders them by dependencies. */
export function groupTargets(servers: ServerView[]): Record<BulkAction, string[]> {
  const names = (pick: (v: ServerView) => boolean) => servers.filter(pick).map((v) => v.name)
  return {
    start: names((v) => v.enabled && !hasProcess(v)),
    stop: names(canStop),
    restart: names(canStop),
  }
}

/** Stops every server, after a confirmation when more than one would stop. */
export function stopAll(servers: ServerView[]) {
  if (servers.filter(canStop).length > 1) openDialog({ kind: 'stopAll' })
  else void bulk('all', 'stop', { all: true })
}

/**
 * Saves the config through `save`, shows the state it answers with, and
 * offers to restart the running servers the save left on their old settings.
 */
/** Saves through save, applies the new state and offers to restart servers the edit made stale. */
export async function saveConfig(save: () => Promise<State>): Promise<State> {
  // Taken before the request: the event stream may deliver the new state before the reply.
  const was = new Set(currentState()?.servers.filter((v) => v.stale).map((v) => v.name))
  const after = await save()
  applyState(after)
  const names = after.servers.filter((v) => v.stale && hasProcess(v) && !was.has(v.name)).map((v) => v.name)
  if (names.length === 0 || !isCurrent(after)) return after
  toast(t('stale.toast', { n: names.length }), 'info', {
    label: t('action.restart'),
    run: () => void bulk('stale', 'restart', { names }),
  })
  return after
}

/** Deletes the output the daemon keeps for a server; terminals showing it start over. */
export const clearScrollback = (name: string) =>
  run(`clear:${name}`, async () => {
    await api.clearLogs(name)
    resetTerminal()
  })

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
