import { useEffect, useState } from 'react'
import { Check, LayoutGrid, List, Monitor, Moon, Sun } from 'lucide-react'
import { swatches } from 'virtual:theme-swatches'
import { appIcon, APP_ICONS, type AppIconStyle } from '../../app/appBridge'
import { useT, type Key } from '../../i18n'
import { useLive } from '../../state/live'
import { setThemeFamily, setThemeMode, usePrefs } from '../../state/prefs'
import { toastError } from '../../state/toasts'
import { FAMILIES, type ModeChoice } from '../../theme/registry'
import { RadioGroup } from '../../ui/RadioGroup'
import { layoutFor, setLayout, useOverviewState } from '../overview/overviewState'
import { SettingGroup, SettingRow } from './rows'

const MODES: { id: ModeChoice; label: Key; icon: React.ReactNode }[] = [
  { id: 'system', label: 'theme.system', icon: <Monitor size={15} aria-hidden="true" /> },
  { id: 'light', label: 'theme.light', icon: <Sun size={15} aria-hidden="true" /> },
  { id: 'dark', label: 'theme.dark', icon: <Moon size={15} aria-hidden="true" /> },
]

const ICON_LABELS: Record<AppIconStyle, Key> = { light: 'theme.light', dark: 'theme.dark' }

/** This browser's theme and overview layout, and in the app its Dock icon. */
export function AppearanceSection() {
  const t = useT()
  const { family, modeChoice, mode } = usePrefs()
  const { layoutChoice } = useOverviewState()
  const count = useLive().state?.servers.length ?? 0
  const layout = layoutFor(layoutChoice, count)

  return (
    <div className="settings">
      <SettingGroup id="set-theme" title={t('settings.theme')}>
        <SettingRow id="set-mode" label={t('settings.appearance')}>
          <RadioGroup className="seg" labelledBy="set-mode-label">
            {MODES.map((m) => (
              <button key={m.id} type="button" role="radio" aria-checked={modeChoice === m.id} tabIndex={modeChoice === m.id ? 0 : -1} className="seg-btn" onClick={() => setThemeMode(m.id)}>
                {m.icon}
                {t(m.label)}
              </button>
            ))}
          </RadioGroup>
        </SettingRow>
        <SettingRow id="set-palette" label={t('settings.palette')} stack>
          <RadioGroup className="families" labelledBy="set-palette-label">
            {FAMILIES.map((f) => {
              const sw = swatches[f[mode]]!
              const selected = family === f.id
              const label = f.id === 'neutral' ? t('theme.neutral') : f.label
              return (
                <button key={f.id} type="button" role="radio" aria-checked={selected} tabIndex={selected ? 0 : -1} className="family" onClick={() => setThemeFamily(f.id)}>
                  <span className="preview" aria-hidden="true" style={{ background: sw.bg, borderColor: sw.border }}>
                    <span className="preview-card" style={{ background: sw.card, borderColor: sw.border }}>
                      <span className="preview-line" style={{ background: sw.text }} />
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
                    {selected && <Check size={14} aria-hidden="true" />}
                  </span>
                </button>
              )
            })}
          </RadioGroup>
        </SettingRow>
      </SettingGroup>

      <SettingGroup id="set-view" title={t('settings.window')}>
        <SettingRow id="set-layout" label={t('settings.layout')} hint={t('settings.layoutHint')}>
          <RadioGroup className="seg" labelledBy="set-layout-label">
            <button type="button" role="radio" aria-checked={layout === 'cards'} tabIndex={layout === 'cards' ? 0 : -1} className="seg-btn" onClick={() => setLayout('cards')}>
              <LayoutGrid size={15} aria-hidden="true" />
              {t('view.cards')}
            </button>
            <button type="button" role="radio" aria-checked={layout === 'list'} tabIndex={layout === 'list' ? 0 : -1} className="seg-btn" onClick={() => setLayout('list')}>
              <List size={15} aria-hidden="true" />
              {t('view.list')}
            </button>
          </RadioGroup>
        </SettingRow>
        {appIcon && <AppIconRow />}
      </SettingGroup>
    </div>
  )
}

/** The app's Dock icon, in the app's window only. */
function AppIconRow() {
  const t = useT()
  const [style, setStyle] = useState(() => appIcon!.current())
  useEffect(() => {
    appIcon!.refresh().then(setStyle, () => {})
  }, [])
  const choose = (next: AppIconStyle) => {
    setStyle(next)
    appIcon!.set(next).then(setStyle, (e) => {
      setStyle(appIcon!.current())
      toastError(e)
    })
  }
  return (
    <SettingRow id="set-icon" label={t('settings.appIcon')}>
      <RadioGroup className="seg" labelledBy="set-icon-label">
        {APP_ICONS.map((s) => (
          <button key={s} type="button" role="radio" aria-checked={style === s} tabIndex={style === s ? 0 : -1} className="seg-btn" onClick={() => choose(s)}>
            <span className={`icon-swatch icon-swatch--${s}`} aria-hidden="true" />
            {t(ICON_LABELS[s])}
          </button>
        ))}
      </RadioGroup>
    </SettingRow>
  )
}
