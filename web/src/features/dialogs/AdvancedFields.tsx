import type { ReactNode } from 'react'
import { useT, type Key } from '../../i18n'
import { TextField } from '../../ui/TextField'
import { HOOKS, type Form, type Hook } from './serverForm'

/** What a server gets for a field it leaves empty: the top-level setting, or its default. */
export interface Inherited {
  stopTimeout: string
  retryAfter: string
  memoryLimit: string
  unreadyAfter: string
  umask: string
  onCrash: string
  onUnready: string
  onRecover: string
}

interface Props {
  form: Form
  set: <K extends keyof Form>(k: K, v: Form[K]) => void
  inherited: Inherited
  open: boolean
  onToggle: (open: boolean) => void
}

const HOOK_LABEL: Record<Hook, Key> = { onCrash: 'settings.onCrash', onUnready: 'settings.onUnready', onRecover: 'settings.onRecover' }

type TextKey = 'readyLog' | 'readyAfter' | 'stopTimeout' | 'retryAfter' | 'memoryLimit' | 'schedule' | 'umask' | 'open' | 'icon' | 'projects' | 'unreadyAfter'
const INHERITED = ['stopTimeout', 'retryAfter', 'memoryLimit', 'unreadyAfter', 'umask'] as const
const isInherited = (k: TextKey): k is (typeof INHERITED)[number] => (INHERITED as readonly string[]).includes(k)

/** The server keys most entries leave alone, grouped in a disclosure below the common fields. */
export function AdvancedFields({ form, set, inherited, open, onToggle }: Props) {
  const t = useT()
  const task = form.oneshot
  const field = (k: TextKey, label: Key, hint: Key, placeholder?: string) => (
    <TextField id={`f-${k}`} label={t(label)} hint={t(hint)} value={form[k]} onChange={(v) => set(k, v)} placeholder={placeholder ?? (isInherited(k) ? inherited[k] : undefined)} />
  )
  return (
    <details className="advanced" open={open} onToggle={(e) => onToggle(e.currentTarget.open)}>
      <summary>{t('form.advanced')}</summary>
      <div className="advanced-body">
        {!task && (
          <Group legend={t('form.readiness')}>
            {field('readyLog', 'form.readyLog', 'form.readyLogHint', 'Listening on')}
            {field('readyAfter', 'form.readyAfter', 'form.readyAfterHint', '10s')}
          </Group>
        )}

        <Group legend={t('form.lifecycle')}>
          {field('stopTimeout', 'settings.stopTimeout', 'settings.stopTimeoutHint')}
          {field('retryAfter', 'settings.retryAfter', 'form.retryAfterHint')}
          {field('memoryLimit', 'settings.memoryLimit', 'form.memoryLimitHint')}
          <div className="field">
            <label className="check">
              <input type="checkbox" checked={form.enabled} onChange={(e) => set('enabled', e.target.checked)} aria-describedby="f-enabled-hint" />
              {t('form.enabled')}
            </label>
            <span className="hint" id="f-enabled-hint">{t('form.enabledHint')}</span>
          </div>
        </Group>

        <Group legend={t('form.scheduleWatch')}>
          {field('schedule', 'form.schedule', 'form.scheduleHint', '0 9 * * mon-fri')}
          <div className="field">
            <label htmlFor="f-watch">{t('form.watch')}</label>
            <textarea id="f-watch" className="textarea textarea--mono" rows={2} value={form.watch} onChange={(e) => set('watch', e.target.value)} spellCheck={false} placeholder="src/**/*.py" aria-describedby="f-watch-hint" />
            <span className="hint" id="f-watch-hint">{t('form.watchHint')}</span>
          </div>
        </Group>

        <Group legend={t('form.hooks')}>
          {HOOKS.map((h) => (
            <HookField key={h} hook={h} label={t(HOOK_LABEL[h])} value={form[h]} inherited={inherited[h]} onChange={(v) => set(h, v)} />
          ))}
          {!task && field('unreadyAfter', 'settings.unreadyAfter', 'settings.unreadyAfterHint')}
        </Group>

        <Group legend={t('form.tools')}>
          <div className="field">
            <label htmlFor="f-wrap">{t('form.wrap')}</label>
            <div className="inline">
              <select id="f-wrap" className="select" value={form.wrap} onChange={(e) => set('wrap', e.target.value as Form['wrap'])}>
                <option value="auto">{t('form.wrapAuto')}</option>
                <option value="none">{t('form.wrapNone')}</option>
                <option value="custom">{t('form.wrapCustom')}</option>
              </select>
              {form.wrap === 'custom' && (
                <input className="input input--mono" aria-label={t('form.wrapCustom')} value={form.wrapCmd} onChange={(e) => set('wrapCmd', e.target.value)} placeholder="nix develop -c" spellCheck={false} />
              )}
            </div>
          </div>
          {field('umask', 'settings.umask', 'settings.umaskHint')}
          <div className="field">
            <label className="check">
              <input type="checkbox" checked={form.lan} onChange={(e) => set('lan', e.target.checked)} aria-describedby="f-lan-hint" />
              {t('form.lan')}
            </label>
            <span className="hint" id="f-lan-hint">{t('form.lanHint')}</span>
          </div>
        </Group>

        <Group legend={t('form.links')}>
          {field('open', 'form.open', 'form.openHint', '/admin')}
          {field('icon', 'form.icon', 'form.iconHint', 'public/favicon.svg')}
          {field('projects', 'form.projects', 'form.projectsHint', 'shop-db')}
        </Group>
      </div>
    </details>
  )
}

function Group({ legend, children }: { legend: string; children: ReactNode }) {
  return (
    <fieldset className="field advanced-group">
      <legend className="label">{legend}</legend>
      <div className="grid-2">{children}</div>
    </fieldset>
  )
}

/** A hook command: empty inherits the top-level one; "None" turns it off for this server. */
function HookField({ hook, label, value, inherited, onChange }: { hook: Hook; label: string; value?: string; inherited: string; onChange: (v?: string) => void }) {
  const t = useT()
  const off = value === ''
  const id = `f-${hook}`
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      <input id={id} className="input input--mono" value={value ?? ''} disabled={off} onChange={(e) => onChange(e.target.value.trim() === '' ? undefined : e.target.value)} placeholder={off ? t('settings.none') : inherited} spellCheck={false} autoComplete="off" />
      <label className="check hint">
        <input type="checkbox" checked={off} onChange={(e) => onChange(e.target.checked ? '' : undefined)} aria-label={`${label}: ${t('form.hookOff')}`} />
        {t('form.hookOff')}
      </label>
    </div>
  )
}
