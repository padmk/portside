import { useState } from 'react'
import { useT } from '../../i18n'
import { useLive } from '../../state/live'
import { closeDialog } from '../../state/ui'
import { Dialog } from '../../ui/Dialog'
import { DiscoverPanel } from './DiscoverPanel'
import { ManualForm } from './ManualForm'
import './server-dialog.css'

interface Props {
  mode: 'discover' | 'manual'
  edit?: string
}

export function ServerDialog({ mode: initial, edit }: Props) {
  const t = useT()
  const { state } = useLive()
  const [mode, setMode] = useState(edit ? 'manual' : initial)

  if (!state) return null
  const title = edit ? t('dialog.editServer', { name: edit }) : t('dialog.addServer')

  return (
    <Dialog title={title} size="lg" className="server-dialog" onClose={closeDialog} plain>
      {!edit && (
        <div className="seg-wrap">
          <div className="seg" role="tablist" aria-label={title}>
            {(['discover', 'manual'] as const).map((m) => (
              <button key={m} type="button" role="tab" aria-selected={mode === m} className="seg-btn" onClick={() => setMode(m)}>
                {t(`dialog.mode.${m}`)}
              </button>
            ))}
          </div>
        </div>
      )}
      {mode === 'discover' && !edit ? (
        <DiscoverPanel state={state} onManual={() => setMode('manual')} />
      ) : (
        <ManualForm key={edit ?? 'new'} state={state} edit={edit} formId="server-form" />
      )}
    </Dialog>
  )
}
