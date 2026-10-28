import { useEffect, useRef, type ReactNode } from 'react'
import { X } from 'lucide-react'
import { useT } from '../i18n'
import './dialog.css'

interface DialogProps {
  title: string
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
  size?: 'sm' | 'md' | 'lg' | 'full'
  /** Dialogs without a visible title bar (the palette) pass `bare`. */
  bare?: boolean
  /** Children render `DialogMain` and `DialogFoot` themselves. */
  plain?: boolean
  className?: string
}

let openDialogs = 0

/** Modal built on <dialog>: the browser traps focus, inerts the page and handles Esc. */
export function Dialog({ title, onClose, children, footer, size = 'md', bare, plain, className = '' }: DialogProps) {
  const t = useT()
  const ref = useRef<HTMLDialogElement>(null)
  const previous = useRef<Element | null>(null)

  useEffect(() => {
    const dlg = ref.current
    if (!dlg) return
    previous.current = document.activeElement
    dlg.showModal()
    ;(dlg.querySelector<HTMLElement>('[data-autofocus]') ?? dlg.querySelector<HTMLElement>('.dialog-main'))?.focus()
    openDialogs++
    document.documentElement.setAttribute('data-scroll-lock', '')
    return () => {
      openDialogs--
      if (openDialogs === 0) document.documentElement.removeAttribute('data-scroll-lock')
      if (previous.current instanceof HTMLElement && previous.current.isConnected) previous.current.focus()
    }
  }, [])

  return (
    <dialog
      ref={ref}
      className={`dialog dialog--${size} ${className}`}
      aria-label={title}
      onCancel={(e) => {
        // React passes a nested dialog's cancel on to this one; that dialog handles its own.
        if (e.target !== ref.current) return
        e.preventDefault()
        onClose()
      }}
      onKeyDown={(e) => {
        // Escape is handled here rather than by the browser, which closes the dialog without a
        // cancelable `cancel` event when there was no click since the previous Escape. A nested
        // dialog's handler has already prevented the default.
        if (e.key !== 'Escape' || e.defaultPrevented || e.nativeEvent.isComposing) return
        e.preventDefault()
        onClose()
      }}
      onMouseDown={(e) => {
        if (e.target === ref.current) onClose()
      }}
    >
      <div className="dialog-body">
        {!bare && (
          <header className="dialog-head">
            <h2>{title}</h2>
            <button type="button" className="icon-btn" aria-label={t('common.close')} title={t('common.close')} onClick={onClose}>
              <X size={16} />
            </button>
          </header>
        )}
        {plain ? children : <DialogMain>{children}</DialogMain>}
        {footer && <DialogFoot>{footer}</DialogFoot>}
      </div>
    </dialog>
  )
}

export const DialogMain = ({ children, className = '' }: { children: ReactNode; className?: string }) => (
  <div className={`dialog-main ${className}`} tabIndex={-1}>{children}</div>
)

export const DialogFoot = ({ children }: { children: ReactNode }) => <footer className="dialog-foot">{children}</footer>
