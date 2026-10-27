import { useCallback, useEffect, useState } from 'react'
import { api, ApiError, errorText } from '../../api/client'
import type { Settings, SettingsView } from '../../api/types'
import { t } from '../../i18n'
import { toast } from '../../state/toasts'
import { envMap, envRows, type EnvRow } from '../../ui/EnvEditor'
import { listOf, listText } from '../../ui/listText'
import { saveConfig } from '../servers/actions'

/** The top-level settings as the inputs edit them: PATH folders one per line, env as rows. */
export type SettingsForm = Omit<Settings, 'path' | 'env'> & { path: string; env: EnvRow[] }

const formOf = (s: Settings): SettingsForm => ({ ...s, path: listText(s.path), env: envRows(s.env) })

const valuesOf = (f: SettingsForm): Settings => ({
  root: f.root.trim(),
  path: listOf(f.path),
  env: envMap(f.env) ?? null,
  restore: f.restore,
  notify: f.notify,
  stopTimeout: f.stopTimeout.trim(),
  shell: f.shell.trim(),
  dockerStart: f.dockerStart.trim(),
  onCrash: f.onCrash.trim(),
  onUnready: f.onUnready.trim(),
  onRecover: f.onRecover.trim(),
  unreadyAfter: f.unreadyAfter.trim(),
  retryAfter: f.retryAfter.trim(),
  memoryLimit: f.memoryLimit.trim(),
  loginEnv: f.loginEnv,
  umask: f.umask.trim(),
})

const same = (a: SettingsForm, b: SettingsForm) => JSON.stringify(valuesOf(a)) === JSON.stringify(valuesOf(b))

export type SettingsFormState = ReturnType<typeof useSettingsForm>

/** The config file's top-level settings, edited in Settings and saved together with one button. */
export function useSettingsForm() {
  const [view, setView] = useState<SettingsView | null>(null)
  const [form, setForm] = useState<SettingsForm | null>(null)
  const [loadError, setLoadError] = useState('')
  const [error, setError] = useState('')
  const [conflict, setConflict] = useState(false)
  const [warnings, setWarnings] = useState<string[]>([])
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    try {
      const v = await api.settings()
      setView(v)
      setForm(formOf(v.values))
      setLoadError('')
      setError('')
      setConflict(false)
    } catch (e) {
      setLoadError(errorText(e) ?? '')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const set = <K extends keyof SettingsForm>(k: K, v: SettingsForm[K]) => {
    setForm((f) => (f ? { ...f, [k]: v } : f))
    setError('')
    setWarnings([])
  }

  const dirty = !!view && !!form && !same(form, formOf(view.values))

  const save = async () => {
    if (!view || !form) return
    setSaving(true)
    setError('')
    setWarnings([])
    try {
      const next = await saveConfig(() => api.saveSettings(valuesOf(form), view.revision))
      await load()
      if (next.warnings?.length) setWarnings(next.warnings)
      else toast(t('settings.saved'), 'info')
    } catch (e) {
      // A conflict keeps the values entered so far; Reload replaces them with the file's.
      if (e instanceof ApiError && e.status === 409) setConflict(true)
      else setError(errorText(e) ?? '')
    } finally {
      setSaving(false)
    }
  }

  return { view, form, set, dirty, save, saving, load, loadError, error, conflict, warnings }
}
