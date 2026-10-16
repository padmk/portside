import { useSyncExternalStore } from 'react'
import { errorText } from '../api/client'

export type ToastKind = 'error' | 'info'

export interface Toast {
  id: number
  kind: ToastKind
  message: string
}

let toasts: Toast[] = []
let nextId = 1
const listeners = new Set<() => void>()

function emit(next: Toast[]) {
  toasts = next
  listeners.forEach((l) => l())
}

export function dismissToast(id: number) {
  emit(toasts.filter((t) => t.id !== id))
}

export function toast(message: string, kind: ToastKind = 'info') {
  const id = nextId++
  emit([...toasts.slice(-3), { id, kind, message }])
  window.setTimeout(() => dismissToast(id), kind === 'error' ? 7000 : 3500)
}

/** Reports a failure; a response from a computer no longer shown is dropped. */
export function toastError(e: unknown) {
  const message = errorText(e)
  if (message !== null) toast(message, 'error')
}

export function useToasts(): Toast[] {
  return useSyncExternalStore(
    (cb) => (listeners.add(cb), () => listeners.delete(cb)),
    () => toasts,
  )
}
