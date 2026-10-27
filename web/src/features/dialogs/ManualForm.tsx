import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Loader2, RotateCw, Shuffle } from 'lucide-react'
import { api, ApiError, errorText } from '../../api/client'
import type { SettingsView, Spec, State } from '../../api/types'
import { useT } from '../../i18n'
import { selectServer } from '../../state/route'
import { closeDialog } from '../../state/ui'
import { toastError } from '../../state/toasts'
import { DialogFoot, DialogMain } from '../../ui/Dialog'
import { EnvEditor } from '../../ui/EnvEditor'
import { saveConfig } from '../servers/actions'
import { NAME_RE } from '../servers/format'
import { AdvancedFields, type Inherited } from './AdvancedFields'
import { blank, defaultRestart, fromSpec, hasAdvanced, toSpec, type Form } from './serverForm'

interface Props {
  state: State
  edit?: string
  formId: string
}

export function ManualForm({ state, edit, formId }: Props) {
  const t = useT()
  const [form, setForm] = useState<Form>(blank)
  const [base, setBase] = useState<Spec | null>(null)
  const [loading, setLoading] = useState(!!edit)
  const [saving, setSaving] = useState(false)
  const [serverError, setServerError] = useState('')
  const [touched, setTouched] = useState(false)
  const [warnings, setWarnings] = useState<string[]>([])
  const [advanced, setAdvanced] = useState(false)
  const [settings, setSettings] = useState<SettingsView | null>(null)
  // The config revision the loaded values come from; a save is refused once the file changed since.
  const [revision, setRevision] = useState<string | undefined>(undefined)
  const [conflict, setConflict] = useState(false)
  const latest = useRef(state)
  latest.current = state

  const load = useCallback((name: string, isLive: () => boolean) => {
    const rev = latest.current.config.revision
    api
      .spec(name)
      .then((s) => {
        if (!isLive()) return
        const next = fromSpec(s)
        setBase(s)
        setForm(next)
        if (hasAdvanced(next)) setAdvanced(true)
        setRevision(rev)
        setConflict(false)
        setServerError('')
        setLoading(false)
      })
      .catch((e) => {
        toastError(e)
        closeDialog()
      })
  }, [])

  useEffect(() => {
    if (!edit) return
    let live = true
    load(edit, () => live)
    return () => {
      live = false
    }
  }, [edit, load])

  // The top-level settings, shown as the placeholders of fields a server inherits.
  useEffect(() => {
    let live = true
    api.settings().then((s) => live && setSettings(s), () => {})
    return () => {
      live = false
    }
  }, [])

  const inherited = useMemo<Inherited>(() => {
    const v = settings?.values
    const d = settings?.defaults
    const none = t('settings.none')
    return {
      stopTimeout: v?.stopTimeout || d?.stopTimeout || '',
      retryAfter: v?.retryAfter || t('settings.never'),
      memoryLimit: v?.memoryLimit || none,
      unreadyAfter: v?.unreadyAfter || d?.unreadyAfter || '',
      umask: v?.umask || t('settings.daemonValue'),
      onCrash: v?.onCrash || none,
      onUnready: v?.onUnready || none,
      onRecover: v?.onRecover || none,
    }
  }, [settings, t])

  const others = useMemo(() => state.servers.filter((s) => s.name !== edit), [state.servers, edit])
  const set = <K extends keyof Form>(k: K, v: Form[K]) => {
    setForm((f) => ({ ...f, [k]: v }))
    setServerError('')
  }
  // A restart policy left at the default follows the default of what the entry becomes; a task cannot use always.
  const setTask = (oneshot: boolean) => {
    setForm((f) => ({
      ...f,
      oneshot,
      restart: f.restart === defaultRestart(f.oneshot) || (oneshot && f.restart === 'always') ? defaultRestart(oneshot) : f.restart,
    }))
    setServerError('')
  }

  const errors = {
    name: !NAME_RE.test(form.name) ? t('form.nameInvalid') : others.some((s) => s.name === form.name) ? t('form.nameTaken') : '',
    cmd: form.cmd.trim() === '' ? t('form.cmdRequired') : '',
    port:
      form.oneshot || form.port === '' || (Number.isInteger(Number(form.port)) && Number(form.port) >= 0 && Number(form.port) <= 65535)
        ? ''
        : t('form.portInvalid'),
  }
  const valid = !errors.name && !errors.cmd && !errors.port
  const shown = (k: keyof typeof errors) => (touched || form[k] !== '') && errors[k]

  const submit = async () => {
    setTouched(true)
    if (!valid) return
    setSaving(true)
    setServerError('')
    const spec = toSpec(form, base)
    try {
      const next = await saveConfig(() => (edit ? api.updateServer(edit, spec, revision) : api.createServer(spec)))
      if (!edit || edit !== spec.name) selectServer(spec.name)
      if (next.warnings?.length) {
        setWarnings(next.warnings)
        setSaving(false)
        return
      }
      closeDialog()
    } catch (e) {
      // A conflict keeps the values entered so far; Reload replaces them with the file's.
      if (e instanceof ApiError && e.status === 409) setConflict(true)
      else setServerError(errorText(e) ?? '')
      setSaving(false)
    }
  }

  const suggestPort = async () => {
    try {
      set('port', String((await api.freePort()).port))
    } catch (e) {
      toastError(e)
    }
  }

  const footer = (
    <DialogFoot>
      <button type="button" className="btn" onClick={closeDialog}>
        {t('common.cancel')}
      </button>
      <button type="submit" form={formId} className="btn btn--primary" disabled={saving || loading}>
        {saving && <Loader2 size={14} className="spin" aria-hidden="true" />}
        {edit ? t('common.save') : t('dialog.addServerButton')}
      </button>
    </DialogFoot>
  )

  if (warnings.length > 0) {
    return (
      <>
        <DialogMain>
          <div role="status">
            <p>{t('form.savedWithWarnings')}</p>
            {warnings.map((warning) => <p className="hint" key={warning}>{warning}</p>)}
          </div>
        </DialogMain>
        <DialogFoot><button type="button" className="btn btn--primary" onClick={closeDialog}>{t('common.close')}</button></DialogFoot>
      </>
    )
  }

  if (loading) {
    return (
      <>
        <DialogMain>
          <p className="scan-state"><Loader2 size={15} className="spin" aria-hidden="true" /> {t('common.loading')}</p>
        </DialogMain>
        {footer}
      </>
    )
  }

  const task = form.oneshot
  return (
    <>
    <DialogMain>
    <form
      id={formId}
      className="manual"
      noValidate
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
    >
      <div className="grid-2">
        <div className="field">
          <label htmlFor="f-name">{t('form.name')}</label>
          <input id="f-name" className="input" value={form.name} onChange={(e) => set('name', e.target.value)} spellCheck={false} autoComplete="off" data-autofocus={edit ? undefined : ''} aria-invalid={!!shown('name')} aria-describedby="f-name-hint" placeholder="web" />
          {shown('name') ? <span className="error" id="f-name-hint">{errors.name}</span> : <span className="hint" id="f-name-hint">{t('form.nameHint', { name: form.name || 'web' })}</span>}
        </div>
        <div className="field">
          <label htmlFor="f-group">{t('form.group')}</label>
          <input id="f-group" className="input" list="f-groups" value={form.group} onChange={(e) => set('group', e.target.value)} placeholder={t('form.groupNone')} autoComplete="off" />
          <datalist id="f-groups">
            {state.groups.map((g) => (
              <option key={g} value={g} />
            ))}
          </datalist>
        </div>
      </div>

      <div className="field">
        <label htmlFor="f-dir">{t('form.dir')}</label>
        <input id="f-dir" className="input input--mono" value={form.dir} onChange={(e) => set('dir', e.target.value)} spellCheck={false} placeholder="~/projects/web" aria-describedby="f-dir-hint" />
        <span className="hint" id="f-dir-hint">{t('form.dirHint', { root: state.config.root })}</span>
      </div>

      <div className="field">
        <label htmlFor="f-cmd">{t('form.cmd')}</label>
        <textarea id="f-cmd" className="textarea textarea--mono" rows={2} value={form.cmd} onChange={(e) => set('cmd', e.target.value)} spellCheck={false} placeholder="pnpm dev --port {PORT}" aria-invalid={!!shown('cmd')} aria-describedby="f-cmd-hint" />
        {shown('cmd') ? <span className="error" id="f-cmd-hint">{errors.cmd}</span> : <span className="hint" id="f-cmd-hint">{t('form.cmdHint')}</span>}
      </div>

      <div className="field">
        <label className="check">
          <input type="checkbox" checked={task} onChange={(e) => setTask(e.target.checked)} aria-describedby="f-task-hint" />
          {t('form.task')}
        </label>
        <span className="hint" id="f-task-hint">{t('form.taskHint')}</span>
      </div>

      <div className="grid-2">
        {!task && (
          <div className="field">
            <label htmlFor="f-port">{t('form.port')}</label>
            <div className="inline">
              <input id="f-port" className="input input--mono" inputMode="numeric" value={form.port} onChange={(e) => set('port', e.target.value.trim())} placeholder={t('form.portAuto')} aria-invalid={!!shown('port')} aria-describedby="f-port-hint" />
              <button type="button" className="btn" onClick={suggestPort}>
                <Shuffle size={14} aria-hidden="true" />
                {t('form.suggestPort')}
              </button>
            </div>
            {shown('port') ? <span className="error" id="f-port-hint">{errors.port}</span> : <span className="hint" id="f-port-hint">{t('form.portHint')}</span>}
          </div>
        )}
        <div className="field">
          <label htmlFor="f-restart">{t('form.restart')}</label>
          <select id="f-restart" className="select" value={form.restart} onChange={(e) => set('restart', e.target.value)}>
            {(['no', 'on-failure', 'always'] as const).map((r) => (
              <option key={r} value={r} disabled={task && r === 'always'}>{t(`restart.${r}`)}</option>
            ))}
          </select>
        </div>
        {!task && (
          <div className="field">
            <label htmlFor="f-health">{t('form.health')}</label>
            <input id="f-health" className="input input--mono" value={form.health} onChange={(e) => set('health', e.target.value)} placeholder="/healthz" spellCheck={false} />
          </div>
        )}
        <fieldset className="field deps-field">
          <legend className="label">{t('form.dependsOn')}</legend>
          {others.length === 0 ? (
            <span className="hint">{t('detail.none')}</span>
          ) : (
            <div className="dep-list">
              {others.map((s) => (
                <label key={s.name} className="dep">
                  <input
                    type="checkbox"
                    checked={form.dependsOn.includes(s.name)}
                    onChange={(e) => set('dependsOn', e.target.checked ? [...form.dependsOn, s.name] : form.dependsOn.filter((d) => d !== s.name))}
                  />
                  <span>{s.name}</span>
                </label>
              ))}
            </div>
          )}
        </fieldset>
      </div>

      <EnvEditor legend={t('form.env')} rows={form.env} onChange={(rows) => set('env', rows)} />

      <AdvancedFields form={form} set={set} inherited={inherited} open={advanced} onToggle={setAdvanced} />

      {serverError && <p className="form-error form-error--block" role="alert">{serverError}</p>}
      {conflict && edit && (
        <div className="form-error form-error--block form-conflict" role="alert">
          <span>{t('conflict.body')}</span>
          <button type="button" className="btn btn--sm" onClick={() => load(edit, () => true)}>
            <RotateCw size={14} aria-hidden="true" />
            {t('conflict.reload')}
          </button>
        </div>
      )}
    </form>
    </DialogMain>
    {footer}
    </>
  )
}
