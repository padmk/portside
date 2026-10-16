import { useEffect, useState } from 'react'
import { ExternalLink, Loader2 } from 'lucide-react'
import { api, errorText } from '../../api/client'
import { useT } from '../../i18n'
import { applyState, useRemote } from '../../state/live'
import { closeDialog } from '../../state/ui'
import { toast } from '../../state/toasts'
import { Dialog, DialogFoot, DialogMain } from '../../ui/Dialog'
import { ConfirmDialog } from '../dialogs/ConfirmDialog'
import { openOnDesktop } from '../servers/actions'
import './config.css'

/** Same as config.Revision in Go: the first 8 bytes of SHA-256, in hex. */
async function revisionOf(text: string): Promise<string> {
  const sum = new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(text)))
  return Array.from(sum.slice(0, 8), (b) => b.toString(16).padStart(2, '0')).join('')
}

export function ConfigDialog() {
  const t = useT()
  const [saved, setSaved] = useState<string | null>(null)
  const [text, setText] = useState('')
  const [path, setPath] = useState('')
  const [revision, setRevision] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [discard, setDiscard] = useState(false)
  const dirty = saved !== null && text !== saved
  const remote = useRemote()

  useEffect(() => {
    let live = true
    api
      .config()
      .then((c) => {
        if (!live) return
        setSaved(c.text)
        setText(c.text)
        setPath(c.path)
        setRevision(c.revision)
      })
      .catch((e) => live && setError(errorText(e) ?? ''))
    return () => {
      live = false
    }
  }, [])

  const save = async () => {
    setSaving(true)
    setError('')
    try {
      applyState(await api.saveConfig(text, revision))
      // The next save is based on exactly the text written now; an edit made
      // elsewhere afterwards changes the file's revision and is detected.
      setRevision(await revisionOf(text))
      setSaved(text)
      toast(t('config.saved'))
    } catch (e) {
      setError(errorText(e) ?? '')
    } finally {
      setSaving(false)
    }
  }

  const close = () => (dirty ? setDiscard(true) : closeDialog())

  return (
    <Dialog title={t('config.title')} size="full" onClose={close} plain>
      <DialogMain className="config-main">
        <p className="config-path mono" title={path}>{path}</p>
        {saved === null && !error ? (
          <p className="scan-state"><Loader2 size={15} className="spin" aria-hidden="true" /> {t('common.loading')}</p>
        ) : (
          <textarea
            className="textarea textarea--mono config-text"
            value={text}
            spellCheck={false}
            aria-label={t('config.title')}
            aria-invalid={!!error}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if ((e.metaKey || e.ctrlKey) && e.key === 's') {
                e.preventDefault()
                if (dirty && !saving) void save()
              }
            }}
          />
        )}
        {error && <pre className="form-error form-error--block config-error" role="alert">{error}</pre>}
      </DialogMain>
      <DialogFoot>
        {!remote && (
          <button type="button" className="btn" onClick={() => openOnDesktop('config')}>
            <ExternalLink size={14} aria-hidden="true" />
            {t('config.openEditor')}
          </button>
        )}
        <span className="foot-spacer" />
        <button type="button" className="btn" onClick={close}>
          {t('common.close')}
        </button>
        <button type="button" className="btn btn--primary" disabled={!dirty || saving} onClick={save}>
          {saving && <Loader2 size={14} className="spin" aria-hidden="true" />}
          {t('common.save')}
        </button>
      </DialogFoot>
      {discard && (
        <ConfirmDialog
          title={t('config.discardTitle')}
          message={t('config.discardBody')}
          confirmLabel={t('config.discard')}
          onConfirm={closeDialog}
          onClose={() => setDiscard(false)}
        />
      )}
    </Dialog>
  )
}
