import { errorText } from '../api/client'
import { createStore, useStore } from './store'

export type ToastKind = 'error' | 'info'

/** A button on a toast; choosing it also dismisses the toast. */
export interface ToastAction {
  label: string
  run: () => void
}

export interface Toast {
  id: number
  kind: ToastKind
  message: string
  action?: ToastAction
}

const store = createStore<Toast[]>([])
let nextId = 1

export function dismissToast(id: number) {
  store.set(store.get().filter((t) => t.id !== id))
}

export function toast(message: string, kind: ToastKind = 'info', action?: ToastAction) {
  const id = nextId++
  store.set([...store.get().slice(-3), { id, kind, message, action }])
  // A toast that offers an action stays long enough to reach it.
  window.setTimeout(() => dismissToast(id), action ? 12000 : kind === 'error' ? 7000 : 3500)
}

/** Reports a failure; a response from a computer no longer shown is dropped. */
export function toastError(e: unknown) {
  const message = errorText(e)
  if (message !== null) toast(message, 'error')
}

export function useToasts(): Toast[] {
  return useStore(store)
}
