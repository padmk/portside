import { useEffect, useState } from 'react'
import { api } from '../../api/client'
import type { AppsView } from '../../api/types'
import { brand, brandTitle } from '../../brand'
import { useT, locales } from '../../i18n'
import { setLocale, usePrefs } from '../../state/prefs'
import { useRemote } from '../../state/live'
import { toastError } from '../../state/toasts'
import { CommandLine } from '../../ui/CommandLine'
import { RadioGroup } from '../../ui/RadioGroup'
import { useLoginStart } from '../servers/LoginStart'
import { SettingGroup, SettingRow, SwitchRow, TextRow } from './rows'
import type { SettingsFormState } from './settingsForm'

/** Startup, the projects folder with the apps that open projects, and the language. */
export function GeneralSection({ settings }: { settings: SettingsFormState }) {
  const t = useT()
  const { locale } = usePrefs()
  const local = !useRemote()
  const login = useLoginStart({ propose: false })
  const { form, view, set } = settings

  return (
    <div className="settings">
      <SettingGroup id="set-startup" title={t('settings.startup')}>
        {local && login && <SwitchRow id="set-login" label={t('empty.login', { name: brandTitle })} hint={t('empty.loginWhy')} checked={login.on} onChange={login.toggle} busy={login.busy} />}
        {local && !login && (
          <SettingRow id="set-service" label={t('settings.service')} hint={t('empty.service', { name: brandTitle })} stack>
            <CommandLine command={`${brand.name} service install`} />
          </SettingRow>
        )}
        {form && <SwitchRow id="set-restore" label={t('settings.restore')} hint={t('settings.restoreHint')} checked={form.restore} onChange={(v) => set('restore', v)} />}
      </SettingGroup>

      <SettingGroup id="set-projects" title={t('settings.projects')}>
        {form && view && <TextRow id="set-root" label={t('settings.root')} hint={t('settings.rootHint')} value={form.root} onChange={(v) => set('root', v)} placeholder={view.defaults.root} />}
        {local && <AppPickers />}
      </SettingGroup>

      <SettingGroup id="set-lang">
        <SettingRow id="set-language" label={t('settings.language')}>
          <RadioGroup className="seg" labelledBy="set-language-label">
            {locales.map((l) => (
              <button key={l.id} type="button" role="radio" aria-checked={locale === l.id} tabIndex={locale === l.id ? 0 : -1} className="seg-btn" onClick={() => setLocale(l.id)}>
                {l.label}
              </button>
            ))}
          </RadioGroup>
        </SettingRow>
      </SettingGroup>
    </div>
  )
}

/** Which editor and terminal "Open in editor" and "Open in Terminal" use on this computer; saved at once. */
function AppPickers() {
  const t = useT()
  const [apps, setApps] = useState<AppsView | null>(null)
  useEffect(() => {
    api.apps().then(setApps, toastError)
  }, [])
  if (!apps) return null
  const save = (key: 'editor' | 'terminal', value: string) => api.setApp(key, value).then(setApps, toastError)
  const picker = (key: 'editor' | 'terminal', value: string, options: AppsView['editors'], auto: string) => {
    const id = `set-${key}`
    // Keep a value written by hand selectable.
    const all = value && !options.some((o) => o.value === value) ? [{ value, label: value }, ...options] : options
    return (
      <SettingRow id={id} label={t(key === 'editor' ? 'settings.editor' : 'settings.terminal')} hint={t(key === 'editor' ? 'settings.editorHint' : 'settings.terminalHint')} field>
        <select id={id} className="select" value={value} onChange={(e) => void save(key, e.target.value)} aria-describedby={`${id}-hint`}>
          <option value="">{auto ? t('settings.automaticApp', { app: auto }) : t('settings.automatic')}</option>
          {all.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      </SettingRow>
    )
  }
  return (
    <>
      {picker('editor', apps.editor, apps.editors, apps.autoEditor)}
      {picker('terminal', apps.terminal, apps.terminals, apps.autoTerminal)}
    </>
  )
}
