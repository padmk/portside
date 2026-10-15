import { useState } from 'react'
import { useT } from '../../i18n'
import { Dialog } from '../../ui/Dialog'
import { toastError } from '../../state/toasts'

interface Props {
  title: string
  message: string
  confirmLabel: string
  danger?: boolean
  onConfirm: () => Promise<unknown> | unknown
  onClose: () => void
}

export function ConfirmDialog({ title, message, confirmLabel, danger = true, onConfirm, onClose }: Props) {
  const t = useT()
  const [busy, setBusy] = useState(false)
  const confirm = async () => {
    setBusy(true)
    try {
      await onConfirm()
      onClose()
    } catch (e) {
      toastError(e)
      setBusy(false)
    }
  }
  return (
    <Dialog
      title={title}
      size="sm"
      onClose={onClose}
      footer={
        <>
          <button type="button" className="btn" onClick={onClose}>
            {t('common.cancel')}
          </button>
          <button type="button" className={`btn ${danger ? 'btn--danger' : 'btn--primary'}`} disabled={busy} onClick={confirm} data-autofocus>
            {confirmLabel}
          </button>
        </>
      }
    >
      <p className="confirm-text">{message}</p>
    </Dialog>
  )
}
