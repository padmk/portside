import { Loader2, RotateCw } from 'lucide-react'
import { brand } from '../../brand'
import { useT, type Key } from '../../i18n'
import { useLive, useRemote } from '../../state/live'
import { openDialog } from '../../state/ui'
import { EnvEditor } from '../../ui/EnvEditor'
import { Switch } from '../../ui/Switch'
import { TextField } from '../../ui/TextField'
import type { SettingsForm, SettingsFormState } from './settingsForm'

/** The Settings sections that edit the config file's top-level keys. */
export type ConfigSection = 'servers' | 'environment' | 'events' | 'docker'

type TextKey = Exclude<{ [K in keyof SettingsForm]: SettingsForm[K] extends string ? K : never }[keyof SettingsForm], 'path'>

export function ConfigSectionPanel({ section, settings }: { section: ConfigSection; settings: SettingsFormState }) {
  const t = useT()
  const { form, view, set } = settings
  if (settings.loadError) return <p className="form-error form-error--block" role="alert">{settings.loadError}</p>
  if (!form || !view) {
    return <p className="scan-state"><Loader2 size={15} className="spin" aria-hidden="true" /> {t('common.loading')}</p>
  }
  const d = view.defaults
  const text = (k: TextKey, label: Key, hint?: Key, placeholder?: string) => (
    <TextField id={`set-${k}`} label={t(label)} hint={hint && t(hint)} value={form[k]} onChange={(v) => set(k, v)} placeholder={placeholder ?? (d[k] || t('settings.none'))} />
  )
  const toggle = (k: 'restore' | 'notify' | 'loginEnv', label: Key, hint?: Key) => (
    <div className="setting setting--switch">
      <Switch checked={form[k]} onChange={(v) => set(k, v)} label={t(label)} describedBy={hint && `set-${k}-hint`} />
      {hint && <p className="setting-hint" id={`set-${k}-hint`}>{t(hint)}</p>}
    </div>
  )

  return (
    <div className="settings settings-form">
      {section === 'servers' && (
        <>
          {text('root', 'settings.root', 'settings.rootHint')}
          {toggle('restore', 'settings.restore')}
          {toggle('notify', 'settings.notify', 'settings.notifyHint')}
          <div className="grid-2">
            {text('stopTimeout', 'settings.stopTimeout', 'settings.stopTimeoutHint')}
            {text('retryAfter', 'settings.retryAfter', 'settings.retryAfterHint', t('settings.never'))}
            {text('memoryLimit', 'settings.memoryLimit', 'settings.memoryLimitHint')}
          </div>
          <PortAndSharing />
        </>
      )}
      {section === 'environment' && (
        <>
          <div className="grid-2">
            {text('shell', 'settings.shell', 'settings.shellHint')}
            {text('umask', 'settings.umask', 'settings.umaskHint', t('settings.daemonValue'))}
          </div>
          <div className="field">
            <label htmlFor="set-path">{t('settings.path')}</label>
            <textarea id="set-path" className="textarea textarea--mono" rows={2} value={form.path} onChange={(e) => set('path', e.target.value)} spellCheck={false} placeholder="/opt/homebrew/opt/node@24/bin" aria-describedby="set-path-hint" />
            <span className="hint" id="set-path-hint">{t('settings.pathHint')}</span>
          </div>
          <EnvEditor legend={t('settings.env')} rows={form.env} onChange={(rows) => set('env', rows)} />
          {toggle('loginEnv', 'settings.loginEnv', 'settings.loginEnvHint')}
        </>
      )}
      {section === 'events' && (
        <>
          <p className="setting-hint">{t('settings.eventsHint', { prefix: brand.name.toUpperCase() })}</p>
          {text('onCrash', 'settings.onCrash')}
          {text('onUnready', 'settings.onUnready')}
          {text('onRecover', 'settings.onRecover')}
          {text('unreadyAfter', 'settings.unreadyAfter', 'settings.unreadyAfterHint')}
        </>
      )}
      {section === 'docker' && text('dockerStart', 'settings.dockerStart', 'settings.dockerStartHint', t('settings.automatic'))}

      {settings.warnings.length > 0 && (
        <div role="status" className="setting-hint">
          <p>{t('settings.savedWithWarnings')}</p>
          {settings.warnings.map((w) => <p key={w}>{w}</p>)}
        </div>
      )}
      {settings.error && <p className="form-error form-error--block" role="alert">{settings.error}</p>}
      {settings.conflict && (
        <div className="form-error form-error--block form-conflict" role="alert">
          <span>{t('conflict.body')}</span>
          <button type="button" className="btn btn--sm" onClick={() => void settings.load()}>
            <RotateCw size={14} aria-hidden="true" />
            {t('conflict.reload')}
          </button>
        </div>
      )}
    </div>
  )
}

/** The daemon's port, set only in the file, and where sharing is turned on. */
function PortAndSharing() {
  const t = useT()
  const port = useLive().state?.daemon.port
  const local = !useRemote()
  return (
    <div className="setting">
      <h3>{t('settings.port')}</h3>
      <p className="setting-hint">
        <code>{port}</code> · {t('settings.portNote')}
      </p>
      {local && (
        <div className="setting-inline">
          <p className="setting-hint">{t('settings.sharing')}</p>
          <button type="button" className="btn btn--sm" onClick={() => openDialog({ kind: 'computers' })}>
            {t('remote.title')}
          </button>
        </div>
      )}
    </div>
  )
}
