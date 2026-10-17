import { useEffect, useRef } from 'react'
import { X } from 'lucide-react'
import { dismissToast, useToasts } from '../state/toasts'
import { useT } from '../i18n'

export function Toasts() {
  const t = useT()
  const items = useToasts()
  const host = useRef<HTMLDivElement>(null)

  // Re-showing the popover moves it above any dialog opened since the last toast.
  useEffect(() => {
    const el = host.current
    if (!el) return
    if (el.matches(':popover-open')) el.hidePopover()
    if (items.length > 0) el.showPopover()
  }, [items])

  return (
    <div className="toasts" popover="manual" ref={host} role="status" aria-live="polite">
      {items.map((x) => (
        <div key={x.id} className={`toast toast--${x.kind}`}>
          <p>{x.message}</p>
          {x.action && (
            <button
              type="button"
              className="btn btn--sm toast-action"
              onClick={() => {
                dismissToast(x.id)
                x.action!.run()
              }}
            >
              {x.action.label}
            </button>
          )}
          <button type="button" className="icon-btn icon-btn--sm" aria-label={t('common.dismiss')} onClick={() => dismissToast(x.id)}>
            <X size={14} />
          </button>
        </div>
      ))}
    </div>
  )
}
