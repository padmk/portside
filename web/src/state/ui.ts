import { useSyncExternalStore } from 'react'

export type DetailTab = 'terminal' | 'containers' | 'details'

export type ContainerTab = 'logs' | 'shell' | 'details' | 'job'

export type DialogState =
  | { kind: 'server'; mode: 'discover' | 'manual'; edit?: string }
  | { kind: 'ports' }
  | { kind: 'config' }
  | { kind: 'palette' }
  | { kind: 'settings' }
  | { kind: 'shortcuts' }
  | { kind: 'computers' }
  | { kind: 'stopAll' }
  | null

interface Ui {
  dialog: DialogState
  tab: DetailTab
  /** Incremented to ask the terminal to take keyboard focus. */
  focusTerminal: number
  /** Incremented to ask the terminal to focus its search field. */
  focusSearch: number
  /** Incremented when a server's scrollback is cleared, so its terminal reconnects to the empty buffer. */
  terminalEpoch: number
  /** The container shown in each containers view, keyed by server name ('' for the containers page). */
  picked: Record<string, string>
  containerTab: ContainerTab
}

let snapshot: Ui = { dialog: null, tab: 'terminal', focusTerminal: 0, focusSearch: 0, terminalEpoch: 0, picked: {}, containerTab: 'logs' }
const listeners = new Set<() => void>()

function publish(next: Partial<Ui>) {
  snapshot = { ...snapshot, ...next }
  listeners.forEach((l) => l())
}

export const openDialog = (dialog: Exclude<DialogState, null>) => publish({ dialog })
export const closeDialog = () => publish({ dialog: null })
export const setTab = (tab: DetailTab) => publish({ tab })
export const showTerminal = () => publish({ tab: 'terminal', focusTerminal: snapshot.focusTerminal + 1 })
export const showTerminalSearch = () => publish({ tab: 'terminal', focusSearch: snapshot.focusSearch + 1 })
export const resetTerminal = () => publish({ terminalEpoch: snapshot.terminalEpoch + 1 })
export const pickContainer = (scope: string, name: string) => publish({ picked: { ...snapshot.picked, [scope]: name } })
export const setContainerTab = (containerTab: ContainerTab) => publish({ containerTab })
export const openAddServer = (mode: 'discover' | 'manual' = 'discover') => openDialog({ kind: 'server', mode })

/** The current UI state, for code outside React render. */
export const getUi = () => snapshot

export function useUi(): Ui {
  return useSyncExternalStore(
    (cb) => (listeners.add(cb), () => listeners.delete(cb)),
    () => snapshot,
  )
}
