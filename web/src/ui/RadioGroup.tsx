import type { ReactNode } from 'react'

interface Props {
  labelledBy: string
  className?: string
  children: ReactNode
}

/** A radio group of buttons (`role="radio"`): arrow keys move the choice, only the checked item is a tab stop. */
export function RadioGroup({ labelledBy, className, children }: Props) {
  return (
    <div
      className={className}
      role="radiogroup"
      aria-labelledby={labelledBy}
      onKeyDown={(e) => {
        const keys = ['ArrowRight', 'ArrowDown', 'ArrowLeft', 'ArrowUp']
        if (!keys.includes(e.key)) return
        const items = Array.from(e.currentTarget.querySelectorAll<HTMLElement>('[role="radio"]'))
        const i = items.indexOf(document.activeElement as HTMLElement)
        if (i < 0) return
        e.preventDefault()
        const delta = e.key === 'ArrowRight' || e.key === 'ArrowDown' ? 1 : -1
        const next = items[(i + delta + items.length) % items.length]
        next?.focus()
        next?.click()
      }}
    >
      {children}
    </div>
  )
}
