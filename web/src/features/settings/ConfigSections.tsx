import { FileCog, FolderOpen } from 'lucide-react'
import { brand } from '../../brand'
import { useT } from '../../i18n'
import { useLive, useRemote } from '../../state/live'
import { openDialog } from '../../state/ui'
import { EnvEditor } from '../../ui/EnvEditor'
import { openOnDesktop } from '../servers/actions'
import { SettingGroup, SettingRow, SwitchRow, TextRow } from './rows'
import type { SettingsForm, SettingsFormState } from './settingsForm'

/** The Settings sections that edit the config file's top-level keys only. */
export type ConfigSection = 'servers' | 'events' | 'environment' | 'advanced'

type TextKey = Exclude<{ [K in keyof SettingsForm]: SettingsForm[K] extends string ? K : never }[keyof SettingsForm], 'path'>

/** `leave` runs a step that closes Settings, after asking about unsaved changes. */
export function ConfigSectionPanel({ section, settings, leave }: { section: ConfigSection; settings: SettingsFormState; leave: (go: () => void) => void }) {
  const t = useT()
  const live = useLive().state
  const local = !useRemote()
  const { form, view, set } = settings
  if (!form || !view) return null
  const d = view.defaults
  const text = (k: TextKey, label: string, hint?: string, placeholder?: string) => (
    <TextRow id={`set-${k}`} label={label} hint={hint} value={form[k]} onChange={(v) => set(k, v)} placeholder={placeholder ?? (String(d[k] || '') || t('settings.none'))} />
  )

  switch (section) {
    case 'servers':
      return (
        <div className="settings">
          <p className="set-intro">{t('settings.serversIntro')}</p>
          <SettingGroup id="set-run" title={t('settings.stopping')}>
            {text('stopTimeout', t('settings.stopTimeout'), t('settings.stopTimeoutHint'))}
            {text('retryAfter', t('settings.retryAfter'), t('settings.retryAfterHint'), t('settings.never'))}
            {text('memoryLimit', t('settings.memoryLimit'), t('settings.memoryLimitHint'))}
          </SettingGroup>
          <SettingGroup id="set-docker" title={t('settings.docker')}>
            {text('dockerStart', t('settings.dockerStart'), t('settings.dockerStartHint'), t('settings.automatic'))}
          </SettingGroup>
        </div>
      )
    case 'events':
      return (
        <div className="settings">
          <SettingGroup id="set-alerts" title={t('settings.alerts')}>
            <SwitchRow id="set-notify" label={t('settings.notify')} hint={t('settings.notifyHint')} checked={form.notify} onChange={(v) => set('notify', v)} />
            {text('unreadyAfter', t('settings.unreadyAfter'), t('settings.unreadyAfterHint'))}
          </SettingGroup>
          <SettingGroup id="set-hooks" title={t('settings.hooks')} note={t('settings.eventsHint', { prefix: brand.name.toUpperCase() })}>
            {text('onCrash', t('settings.onCrash'), t('settings.onCrashHint'))}
            {text('onUnready', t('settings.onUnready'), t('settings.onUnreadyHint'))}
            {text('onRecover', t('settings.onRecover'), t('settings.onRecoverHint'))}
          </SettingGroup>
        </div>
      )
    case 'environment':
      return (
        <div className="settings">
          <p className="set-intro">{t('settings.environmentIntro')}</p>
          <SettingGroup id="set-shell" title={t('settings.shellGroup')}>
            {text('shell', t('settings.shell'), t('settings.shellHint'))}
            <SwitchRow id="set-loginEnv" label={t('settings.loginEnv')} hint={t('settings.loginEnvHint')} checked={form.loginEnv} onChange={(v) => set('loginEnv', v)} />
            {text('umask', t('settings.umask'), t('settings.umaskHint'), t('settings.daemonValue'))}
          </SettingGroup>
          <SettingGroup id="set-vars" title={t('settings.variables')}>
            <SettingRow id="set-path" label={t('settings.path')} hint={t('settings.pathHint')} field>
              <textarea id="set-path" className="textarea textarea--mono" rows={2} value={form.path} onChange={(e) => set('path', e.target.value)} spellCheck={false} placeholder="/opt/homebrew/opt/node@24/bin" aria-describedby="set-path-hint" />
            </SettingRow>
            <div className="set-row set-row--stack">
              <EnvEditor legend={t('settings.env')} rows={form.env} onChange={(rows) => set('env', rows)} />
            </div>
          </SettingGroup>
        </div>
      )
    case 'advanced':
      return (
        <div className="settings">
          <SettingGroup id="set-daemon" title={t('settings.daemon')}>
            {text('port', t('settings.port'), t('settings.portHint', { port: live?.daemon.port ?? '' }), t('settings.automatic'))}
            {local && (
              <SettingRow id="set-computers" label={t('remote.title')} hint={t('settings.sharing')}>
                <button type="button" className="btn btn--sm" onClick={() => leave(() => openDialog({ kind: 'computers' }))}>
                  {t('settings.openComputers')}
                </button>
              </SettingRow>
            )}
          </SettingGroup>
          <SettingGroup id="set-files" title={t('settings.files')}>
            <SettingRow id="set-config" label={t('settings.configFile')} hint={<code className="set-path">{live?.config.pathDisplay}</code>}>
              <div className="set-actions">
                <button type="button" className="btn btn--sm" onClick={() => leave(() => openDialog({ kind: 'config' }))}>
                  <FileCog size={14} aria-hidden="true" />
                  {t('settings.editHere')}
                </button>
                {local && (
                  <button type="button" className="btn btn--sm" onClick={() => openOnDesktop('config')}>
                    {t('config.openEditor')}
                  </button>
                )}
              </div>
            </SettingRow>
            {local && (
              <SettingRow id="set-logs" label={t('settings.logs')} hint={t('settings.logsHint')}>
                <button type="button" className="btn btn--sm" onClick={() => openOnDesktop('logs')}>
                  <FolderOpen size={14} aria-hidden="true" />
                  {t('settings.showLogs')}
                </button>
              </SettingRow>
            )}
          </SettingGroup>
        </div>
      )
  }
}
