import { Check, Copy } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useT } from '../i18n'
import { copyText } from './hooks'

/** A copy button that confirms with a check mark; children render before the icon. */
export function CopyButton({
  text,
  label,
  className = 'icon-btn icon-btn--sm',
  size = 15,
  children,
}: {
  text: string
  label: string
  className?: string
  size?: number
  children?: ReactNode
}) {
  const t = useT()
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      className={className}
      aria-label={label}
      title={t('common.copy')}
      onClick={async (e) => {
        e.stopPropagation()
        await copyText(text)
        setDone(true)
        window.setTimeout(() => setDone(false), 1200)
      }}
    >
      {children}
      {done ? <Check size={size} aria-hidden="true" /> : <Copy size={size} aria-hidden="true" />}
    </button>
  )
}
