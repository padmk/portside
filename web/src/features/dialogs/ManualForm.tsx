import { useEffect, useMemo, useState } from 'react'
import { Loader2, Plus, Shuffle, X } from 'lucide-react'
import { api, errorText } from '../../api/client'
import type { Spec, State } from '../../api/types'
import { useT } from '../../i18n'
import { applyState } from '../../state/live'
import { selectServer } from '../../state/route'
import { closeDialog } from '../../state/ui'
import { toastError } from '../../state/toasts'
import { DialogFoot, DialogMain } from '../../ui/Dialog'
import { NAME_RE } from '../servers/format'

interface Form {
  name: string
  dir: string
  cmd: string
  port: string
  group: string
  dependsOn: string[]
  restart: string
  health: string
  env: { k: string; v: string }[]
}

const blank: Form = { name: '', dir: '', cmd: '', port: '', group: '', dependsOn: [], restart: 'no', health: '', env: [] }

const fromSpec = (s: Spec): Form => ({
  name: s.name,
  dir: s.dir,
  cmd: s.cmd,
  port: s.port === undefined ? '' : String(s.port),
  group: s.group ?? '',
  dependsOn: s.dependsOn ?? [],
  restart: s.restart || 'no',
  health: s.health ?? '',
  env: Object.entries(s.env ?? {}).map(([k, v]) => ({ k, v })),
})

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

  useEffect(() => {
    if (!edit) return
    let live = true
    api
      .spec(edit)
      .then((s) => {
        if (!live) return
        setBase(s)
        setForm(fromSpec(s))
        setLoading(false)
      })
      .catch((e) => {
        toastError(e)
        closeDialog()
      })
    return () => {
      live = false
    }
  }, [edit])

  const others = useMemo(() => state.servers.filter((s) => s.name !== edit), [state.servers, edit])
  const set = <K extends keyof Form>(k: K, v: Form[K]) => {
    setForm((f) => ({ ...f, [k]: v }))
    setServerError('')
  }

  const errors = {
    name: !NAME_RE.test(form.name) ? t('form.nameInvalid') : others.some((s) => s.name === form.name) ? t('form.nameTaken') : '',
    cmd: form.cmd.trim() === '' ? t('form.cmdRequired') : '',
    port:
      form.port === '' || (Number.isInteger(Number(form.port)) && Number(form.port) >= 0 && Number(form.port) <= 65535)
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
    const env: Record<string, string> = {}
    for (const { k, v } of form.env) if (k.trim()) env[k.trim()] = v
    const spec: Spec = {
      ...base,
      name: form.name,
      dir: form.dir.trim(),
      cmd: form.cmd.trim(),
      port: form.port === '' ? undefined : Number(form.port),
      group: form.group.trim() || undefined,
      dependsOn: form.dependsOn.length ? form.dependsOn : undefined,
      restart: form.restart === 'no' ? undefined : form.restart,
      health: form.health.trim() || undefined,
      env: Object.keys(env).length ? env : undefined,
    }
    try {
      const next = edit ? await api.updateServer(edit, spec) : await api.createServer(spec)
      applyState(next)
      if (!edit || edit !== spec.name) selectServer(spec.name)
      closeDialog()
    } catch (e) {
      setServerError(errorText(e) ?? '')
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

      <div className="grid-2">
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
        <div className="field">
          <label htmlFor="f-restart">{t('form.restart')}</label>
          <select id="f-restart" className="select" value={form.restart} onChange={(e) => set('restart', e.target.value)}>
            {(['no', 'on-failure', 'always'] as const).map((r) => (
              <option key={r} value={r}>{t(`restart.${r}`)}</option>
            ))}
          </select>
        </div>
      </div>

      <div className="grid-2">
        <div className="field">
          <label htmlFor="f-health">{t('form.health')}</label>
          <input id="f-health" className="input input--mono" value={form.health} onChange={(e) => set('health', e.target.value)} placeholder="/healthz" spellCheck={false} />
        </div>
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

      <fieldset className="field env-field">
        <legend className="label">{t('form.env')}</legend>
        {form.env.map((row, i) => (
          <div className="env-row" key={i}>
            <input className="input input--mono" value={row.k} aria-label={t('form.envKey')} placeholder="KEY" spellCheck={false} onChange={(e) => set('env', form.env.map((r, j) => (j === i ? { ...r, k: e.target.value } : r)))} />
            <input className="input input--mono" value={row.v} aria-label={t('form.envValue')} placeholder="value" spellCheck={false} onChange={(e) => set('env', form.env.map((r, j) => (j === i ? { ...r, v: e.target.value } : r)))} />
            <button type="button" className="icon-btn" aria-label={t('form.envRemove')} title={t('form.envRemove')} onClick={() => set('env', form.env.filter((_, j) => j !== i))}>
              <X size={15} />
            </button>
          </div>
        ))}
        <button type="button" className="btn btn--ghost env-add" onClick={() => set('env', [...form.env, { k: '', v: '' }])}>
          <Plus size={14} aria-hidden="true" />
          {t('form.envAdd')}
        </button>
      </fieldset>

      {serverError && <p className="form-error form-error--block" role="alert">{serverError}</p>}
    </form>
    </DialogMain>
    {footer}
    </>
  )
}
