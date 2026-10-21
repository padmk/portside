import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react'

const CloseContext = createContext<(restoreFocus?: boolean) => void>(() => {})

interface MenuProps {
  label: string
  trigger: ReactNode
  children: ReactNode
  className?: string
}

export function Menu({ label, trigger, children, className = 'icon-btn' }: MenuProps) {
  const [open, setOpen] = useState(false)
  const wrap = useRef<HTMLDivElement>(null)
  const button = useRef<HTMLButtonElement>(null)
  const list = useRef<HTMLDivElement>(null)

  const close = useCallback((restoreFocus = true) => {
    setOpen(false)
    if (restoreFocus) button.current?.focus()
  }, [])

  useEffect(() => {
    if (!open) return
    list.current?.querySelector<HTMLElement>('[role^="menuitem"]:not(:disabled)')?.focus()
    const onDown = (e: PointerEvent) => {
      if (!wrap.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('pointerdown', onDown)
    return () => document.removeEventListener('pointerdown', onDown)
  }, [open])

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.stopPropagation()
      close()
      return
    }
    if (e.key === 'Tab') {
      setOpen(false)
      return
    }
    const items = Array.from(list.current?.querySelectorAll<HTMLElement>('[role^="menuitem"]:not(:disabled)') ?? [])
    const i = items.indexOf(document.activeElement as HTMLElement)
    let next = -1
    if (e.key === 'ArrowDown') next = (i + 1) % items.length
    else if (e.key === 'ArrowUp') next = (i - 1 + items.length) % items.length
    else if (e.key === 'Home') next = 0
    else if (e.key === 'End') next = items.length - 1
    if (next >= 0) {
      e.preventDefault()
      e.stopPropagation()
      items[next]?.focus()
    }
  }

  return (
    <div className="menu-wrap" ref={wrap}>
      <button
        ref={button}
        type="button"
        className={className}
        aria-label={label}
        title={label}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
      >
        {trigger}
      </button>
      {open && (
        <div className="menu" role="menu" aria-label={label} ref={list} onKeyDown={onKeyDown}>
          <CloseContext value={close}>{children}</CloseContext>
        </div>
      )}
    </div>
  )
}

interface ItemProps {
  icon?: ReactNode
  children: ReactNode
  onSelect: () => void
  danger?: boolean
  disabled?: boolean
  trail?: ReactNode
  /** Renders the item as a radio option. */
  checked?: boolean
}

export function MenuItem({ icon, children, onSelect, danger, disabled, trail, checked }: ItemProps) {
  const close = useContext(CloseContext)
  return (
    <button
      type="button"
      role={checked === undefined ? 'menuitem' : 'menuitemradio'}
      aria-checked={checked}
      className={`menu-item${danger ? ' menu-item--danger' : ''}`}
      disabled={disabled}
      onClick={() => {
        close(false)
        onSelect()
      }}
    >
      {icon}
      <span>{children}</span>
      {trail && <span className="trail">{trail}</span>}
    </button>
  )
}

export const MenuSeparator = () => <div className="menu-sep" role="separator" />
