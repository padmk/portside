import { useState, type KeyboardEvent } from 'react'
import { Keyboard, Loader2, RotateCw } from 'lucide-react'
import { useT, type Key } from '../../i18n'
import { useLive, useRemote } from '../../state/live'
import { closeDialog, getUi, openDialog } from '../../state/ui'
import { Dialog, DialogFoot, DialogMain } from '../../ui/Dialog'
import '../../ui/fields.css'
import { AgentSetup } from '../agents/AgentSetup'
import { ConfirmDialog } from '../dialogs/ConfirmDialog'
import { AppearanceSection } from './AppearanceSection'
import { ConfigSectionPanel, type ConfigSection } from './ConfigSections'
import { GeneralSection } from './GeneralSection'
import { useSettingsForm, type SettingsFormState } from './settingsForm'
import './settings.css'

type Section = 'general' | 'appearance' | ConfigSection | 'agents'

const LABELS: Record<Section, Key> = {
  general: 'settings.tab.general',
  appearance: 'settings.tab.appearance',
  servers: 'settings.tab.servers',
  events: 'settings.tab.events',
  environment: 'settings.tab.environment',
  agents: 'agents.title',
  advanced: 'settings.tab.advanced',
}

const isConfig = (s: Section): s is ConfigSection => s === 'servers' || s === 'events' || s === 'environment' || s === 'advanced'

export function SettingsDialog() {
  const t = useT()
  const local = !useRemote()
  const version = useLive().state?.daemon.version
  const settings = useSettingsForm()
  const sections: Section[] = ['general', 'appearance', 'servers', 'events', 'environment', ...(local ? (['agents'] as const) : []), 'advanced']
  // Opened for the coding agents (the menu bar app's Connect Coding Agents): that section is shown.
  const [toAgents] = useState(() => {
    const d = getUi().dialog
    return d?.kind === 'settings' && d.section === 'agents'
  })
  const [section, setSection] = useState<Section>(toAgents && local ? 'agents' : 'general')
  // A step that closes Settings while there are unsaved changes, waiting for the user to confirm.
  const [pending, setPending] = useState<(() => void) | null>(null)
  const leave = (go: () => void) => (settings.dirty ? setPending(() => go) : go())

  // Arrow keys, Home and End move between the tabs and show the one focused.
  const onTabKey = (e: KeyboardEvent<HTMLDivElement>) => {
    const i = sections.indexOf(section)
    const to: Record<string, number> = { ArrowDown: i + 1, ArrowRight: i + 1, ArrowUp: i - 1, ArrowLeft: i - 1, Home: 0, End: sections.length - 1 }
    if (!(e.key in to)) return
    e.preventDefault()
    const next = sections[(to[e.key]! + sections.length) % sections.length]!
    setSection(next)
    e.currentTarget.querySelector<HTMLElement>(`#set-tab-${next}`)?.focus()
  }

  return (
    <Dialog title={t('settings.title')} size="lg" className="settings-dialog" onClose={() => leave(closeDialog)} plain>
      <div className="settings-layout">
        <div className="settings-tabs" role="tablist" aria-orientation="vertical" aria-label={t('settings.sections')} onKeyDown={onTabKey}>
          {sections.map((s) => (
            <button
              key={s}
              id={`set-tab-${s}`}
              type="button"
              role="tab"
              aria-selected={section === s}
              aria-controls="set-panel"
              tabIndex={section === s ? 0 : -1}
              className="settings-tab"
              onClick={() => setSection(s)}
            >
              {t(LABELS[s])}
            </button>
          ))}
        </div>
        <DialogMain className="settings-panel">
          <div id="set-panel" role="tabpanel" aria-labelledby={`set-tab-${section}`}>
            {settings.loadError && isConfig(section) ? (
              <p className="form-error form-error--block" role="alert">{settings.loadError}</p>
            ) : (
              <>
                {section === 'general' && <GeneralSection settings={settings} />}
                {section === 'appearance' && <AppearanceSection />}
                {isConfig(section) && <ConfigSectionPanel section={section} settings={settings} leave={leave} />}
                {section === 'agents' && (
                  <div className="settings" id="set-agents">
                    <p className="set-intro">{t('agents.body')}</p>
                    <AgentSetup reveal={toAgents} />
                  </div>
                )}
              </>
            )}
            <SaveStatus settings={settings} />
          </div>
        </DialogMain>
      </div>
      <DialogFoot>
        {version && <span className="foot-note">{t('settings.version', { version })}</span>}
        <a className="foot-link" href="/licenses.txt" target="_blank" rel="noreferrer">
          {t('settings.licenses')}
        </a>
        <button type="button" className="btn btn--ghost btn--sm" onClick={() => leave(() => openDialog({ kind: 'shortcuts' }))}>
          <Keyboard size={15} aria-hidden="true" />
          {t('menu.shortcuts')}
        </button>
        <span className="foot-spacer" />
        {settings.dirty && <span className="foot-note settings-unsaved">{t('settings.unsaved')}</span>}
        <button type="button" className="btn btn--primary" disabled={!settings.dirty || settings.saving} onClick={() => void settings.save()}>
          {settings.saving && <Loader2 size={14} className="spin" aria-hidden="true" />}
          {t('common.save')}
        </button>
      </DialogFoot>
      {pending && (
        <ConfirmDialog
          title={t('config.discardTitle')}
          message={t('settings.discardBody')}
          confirmLabel={t('config.discard')}
          onConfirm={pending}
          onClose={() => setPending(null)}
        />
      )}
    </Dialog>
  )
}

/** What the last save left to say: its warnings, an error, or a conflict with a change made elsewhere. */
function SaveStatus({ settings }: { settings: SettingsFormState }) {
  const t = useT()
  return (
    <>
      {settings.warnings.length > 0 && (
        <div role="status" className="set-status">
          <p>{t('settings.savedWithWarnings')}</p>
          {settings.warnings.map((w) => <p key={w}>{w}</p>)}
        </div>
      )}
      {settings.error && <p className="form-error form-error--block set-status" role="alert">{settings.error}</p>}
      {settings.conflict && (
        <div className="form-error form-error--block form-conflict set-status" role="alert">
          <span>{t('conflict.body')}</span>
          <button type="button" className="btn btn--sm" onClick={() => void settings.load()}>
            <RotateCw size={14} aria-hidden="true" />
            {t('conflict.reload')}
          </button>
        </div>
      )}
    </>
  )
}
