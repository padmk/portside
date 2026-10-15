import { useSyncExternalStore } from 'react'

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

export const toastError = (e: unknown) => toast(e instanceof Error ? e.message : String(e), 'error')

export function useToasts(): Toast[] {
  return useSyncExternalStore(
    (cb) => (listeners.add(cb), () => listeners.delete(cb)),
    () => toasts,
  )
}
