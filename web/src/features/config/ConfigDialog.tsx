import { useCallback, useEffect, useRef, useState } from 'react'
import { ExternalLink, Loader2 } from 'lucide-react'
import { api, ApiError, errorText } from '../../api/client'
import { useT } from '../../i18n'
import { useLive } from '../../state/live'
import { closeDialog } from '../../state/ui'
import { toast } from '../../state/toasts'
import { Dialog, DialogFoot, DialogMain } from '../../ui/Dialog'
import { ConfirmDialog } from '../dialogs/ConfirmDialog'
import { openOnDesktop, saveConfig } from '../servers/actions'
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
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [discard, setDiscard] = useState(false)
  // The file changed since the text was loaded or saved, while there were edits here.
  const [conflict, setConflict] = useState(false)
  const dirty = saved !== null && text !== saved
  const { state, remote } = useLive()
  const liveRevision = state?.config.revision
  // The revision the editor's text is based on; a save is refused once the file has another.
  const base = useRef('')
  const dirtyRef = useRef(dirty)
  dirtyRef.current = dirty

  const load = useCallback(async () => {
    try {
      const c = await api.config()
      base.current = c.revision
      setSaved(c.text)
      setText(c.text)
      setPath(c.path)
      setConflict(false)
      setError('')
    } catch (e) {
      setError(errorText(e) ?? '')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  // An edit made elsewhere while the editor is open: take it when nothing here
  // would be lost, otherwise ask.
  useEffect(() => {
    if (!liveRevision || !base.current || liveRevision === base.current) return
    if (dirtyRef.current) setConflict(true)
    else void load()
  }, [liveRevision, load])

  /** Saves the text; `overwrite` replaces whatever the file holds now. */
  const save = async (overwrite = false) => {
    setSaving(true)
    setError('')
    const prev = base.current
    try {
      if (overwrite) base.current = (await api.config()).revision
      const from = base.current
      // Set before the save lands, so the state it brings is not taken for an edit made elsewhere.
      base.current = await revisionOf(text)
      await saveConfig(() => api.saveConfig(text, from))
      setSaved(text)
      setConflict(false)
      toast(t('config.saved'))
    } catch (e) {
      base.current = prev
      if (e instanceof ApiError && e.status === 409) setConflict(true)
      else setError(errorText(e) ?? '')
    } finally {
      setSaving(false)
    }
  }

  const close = () => (dirty ? setDiscard(true) : closeDialog())

  return (
    <Dialog title={t('config.title')} size="full" onClose={close} plain>
      <DialogMain className="config-main">
        <p className="config-path mono" title={path}>{path}</p>
        {conflict && (
          <div className="form-error form-error--block config-conflict" role="alert">
            <span>{t('conflict.body')}</span>
            <span className="config-conflict-actions">
              <button type="button" className="btn btn--sm" disabled={saving} onClick={() => void load()}>
                {t('config.reloadDiscard')}
              </button>
              <button type="button" className="btn btn--sm" disabled={saving} onClick={() => void save(true)}>
                {t('config.overwrite')}
              </button>
            </span>
          </div>
        )}
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
                if (dirty && !saving && !conflict) void save()
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
        <button type="button" className="btn btn--primary" disabled={!dirty || saving || conflict} onClick={() => void save()}>
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
