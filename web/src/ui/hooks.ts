import { useCallback, useEffect, useRef, useState } from 'react'
import { toast } from '../state/toasts'
import { t } from '../i18n'

/** Re-renders every `ms` and returns the current time. */
export function useNow(ms = 1000): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), ms)
    return () => window.clearInterval(id)
  }, [ms])
  return now
}

export async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    document.execCommand('copy')
    ta.remove()
  }
  toast(t('common.copied'))
}

/** Tracks one in-flight async action and guards against double submission. */
export function useAction<A extends unknown[]>(fn: (...args: A) => Promise<unknown>) {
  const [busy, setBusy] = useState(false)
  const alive = useRef(true)
  useEffect(() => () => void (alive.current = false), [])
  const run = useCallback(
    async (...args: A) => {
      setBusy(true)
      try {
        return await fn(...args)
      } finally {
        if (alive.current) setBusy(false)
      }
    },
    [fn],
  )
  return [run, busy] as const
}
