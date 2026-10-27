import { useEffect, useState } from 'react'
import { Check, Keyboard, LayoutGrid, List, Monitor, Moon, Sun } from 'lucide-react'
import { api } from '../../api/client'
import type { AppsView } from '../../api/types'
import { swatches } from 'virtual:theme-swatches'
import { useT, locales, type Key } from '../../i18n'
import { setLocale, setThemeFamily, setThemeMode, usePrefs } from '../../state/prefs'
import { closeDialog, getUi, openDialog } from '../../state/ui'
import { FAMILIES, type ModeChoice } from '../../theme/registry'
import { Dialog } from '../../ui/Dialog'
import { RadioGroup } from '../../ui/RadioGroup'
import { layoutFor, setLayout, useOverviewState } from '../overview/overviewState'
import { useLive, useRemote } from '../../state/live'
import { toastError } from '../../state/toasts'
import { AgentSetup } from '../agents/AgentSetup'
import { LoginStartSwitch, useLoginStart } from '../servers/LoginStart'
import { brand, brandTitle } from '../../brand'
import { CommandLine } from '../../ui/CommandLine'
import './settings.css'

const MODES: { id: ModeChoice; label: Key; icon: React.ReactNode }[] = [
  { id: 'system', label: 'theme.system', icon: <Monitor size={15} aria-hidden="true" /> },
  { id: 'light', label: 'theme.light', icon: <Sun size={15} aria-hidden="true" /> },
  { id: 'dark', label: 'theme.dark', icon: <Moon size={15} aria-hidden="true" /> },
]

export function SettingsDialog() {
  const t = useT()
  const { family, modeChoice, mode, locale } = usePrefs()
  const { layoutChoice } = useOverviewState()
  const count = useLive().state?.servers.length ?? 0
  const layout = layoutFor(layoutChoice, count)
  const local = !useRemote()
  const login = useLoginStart({ propose: false })
  // Opened for the coding agents (the menu bar app's Connect Coding Agents): that section is scrolled into view.
  const [toAgents] = useState(() => {
    const d = getUi().dialog
    return d?.kind === 'settings' && d.section === 'agents'
  })

  return (
    <Dialog
      title={t('settings.title')}
      size="md"
      onClose={closeDialog}
      footer={
        <>
          <span className="foot-note">{t('settings.shortcutsHint')}</span>
          <span className="foot-spacer" />
          <a className="foot-link" href="/licenses.txt" target="_blank" rel="noreferrer">
            {t('settings.licenses')}
          </a>
          <button type="button" className="btn" onClick={() => openDialog({ kind: 'shortcuts' })}>
            <Keyboard size={16} aria-hidden="true" />
            {t('menu.shortcuts')}
          </button>
        </>
      }
    >
      <div className="settings">
        <section className="setting" aria-labelledby="set-appearance">
          <h3 id="set-appearance">{t('settings.appearance')}</h3>
          <RadioGroup className="seg" labelledBy="set-appearance">
            {MODES.map((m) => (
              <button key={m.id} type="button" role="radio" aria-checked={modeChoice === m.id} tabIndex={modeChoice === m.id ? 0 : -1} className="seg-btn" onClick={() => setThemeMode(m.id)}>
                {m.icon}
                {t(m.label)}
              </button>
            ))}
          </RadioGroup>
        </section>

        <section className="setting" aria-labelledby="set-palette">
          <h3 id="set-palette">{t('settings.palette')}</h3>
          <RadioGroup className="families" labelledBy="set-palette">
            {FAMILIES.map((f) => {
              const sw = swatches[f[mode]]!
              const selected = family === f.id
              const label = f.id === 'neutral' ? t('theme.neutral') : f.label
              return (
                <button key={f.id} type="button" role="radio" aria-checked={selected} tabIndex={selected ? 0 : -1} className="family" onClick={() => setThemeFamily(f.id)}>
                  <span className="preview" aria-hidden="true" style={{ background: sw.bg, borderColor: sw.border }}>
                    <span className="preview-card" style={{ background: sw.card, borderColor: sw.border }}>
                      <span className="preview-line" style={{ background: sw.text }} />
                      <span className="preview-line preview-line--short" style={{ background: sw.text, opacity: 0.45 }} />
                      <span className="preview-lamps">
                        <i style={{ background: sw.running }} />
                        <i style={{ background: sw.starting }} />
                        <i style={{ background: sw.crashed, borderRadius: 2, transform: 'rotate(45deg) scale(0.86)' }} />
                        <i style={{ boxShadow: `inset 0 0 0 2px ${sw.external}` }} />
                      </span>
                    </span>
                    <span className="preview-btn" style={{ background: sw.primary, color: sw['primary-fg'] }}>
                      <b />
                    </span>
                  </span>
                  <span className="family-name">
                    {label}
                    {selected && <Check size={15} aria-hidden="true" />}
                  </span>
                </button>
              )
            })}
          </RadioGroup>
        </section>

        <div className="setting-row">
          <section className="setting" aria-labelledby="set-layout">
            <h3 id="set-layout">{t('settings.layout')}</h3>
            <RadioGroup className="seg" labelledBy="set-layout">
              <button type="button" role="radio" aria-checked={layout === 'cards'} tabIndex={layout === 'cards' ? 0 : -1} className="seg-btn" onClick={() => setLayout('cards')}>
                <LayoutGrid size={15} aria-hidden="true" />
                {t('view.cards')}
              </button>
              <button type="button" role="radio" aria-checked={layout === 'list'} tabIndex={layout === 'list' ? 0 : -1} className="seg-btn" onClick={() => setLayout('list')}>
                <List size={15} aria-hidden="true" />
                {t('view.list')}
              </button>
            </RadioGroup>
          </section>
          <section className="setting" aria-labelledby="set-language">
            <h3 id="set-language">{t('settings.language')}</h3>
            <RadioGroup className="seg" labelledBy="set-language">
              {locales.map((l) => (
                <button key={l.id} type="button" role="radio" aria-checked={locale === l.id} tabIndex={locale === l.id ? 0 : -1} className="seg-btn" onClick={() => setLocale(l.id)}>
                  {l.label}
                </button>
              ))}
            </RadioGroup>
          </section>
        </div>

        {local && <AppPickers />}

        {local && (
          <section className="setting" aria-labelledby="set-startup">
            <h3 id="set-startup">{t('settings.startup')}</h3>
            {login ? (
              <LoginStartSwitch login={login} id="set-startup-why" className="setting-login" />
            ) : (
              <>
                <p className="setting-hint">{t('empty.service', { name: brandTitle })}</p>
                <CommandLine command={`${brand.name} service install`} />
              </>
            )}
          </section>
        )}

        {local && (
          <section className="setting" id="set-agents" aria-labelledby="set-agents-title">
            <h3 id="set-agents-title">{t('agents.title')}</h3>
            <p className="setting-hint">{t('agents.body')}</p>
            <AgentSetup reveal={toAgents} />
          </section>
        )}
      </div>
    </Dialog>
  )
}

/** Which editor and terminal "Open in editor" and "Open in Terminal" use on this computer. */
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
      <section className="setting setting--app">
        <h3>
          <label htmlFor={id}>{t(key === 'editor' ? 'settings.editor' : 'settings.terminal')}</label>
        </h3>
        <select id={id} className="select" value={value} onChange={(e) => void save(key, e.target.value)}>
          <option value="">{auto ? t('settings.automaticApp', { app: auto }) : t('settings.automatic')}</option>
          {all.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      </section>
    )
  }
  return (
    <div className="setting-row">
      {picker('editor', apps.editor, apps.editors, apps.autoEditor)}
      {picker('terminal', apps.terminal, apps.terminals, apps.autoTerminal)}
    </div>
  )
}
