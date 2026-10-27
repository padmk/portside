import { useState, type KeyboardEvent } from 'react'
import { Keyboard, Loader2 } from 'lucide-react'
import { useT, type Key } from '../../i18n'
import { useLive, useRemote } from '../../state/live'
import { closeDialog, getUi, openDialog } from '../../state/ui'
import { Dialog, DialogFoot, DialogMain } from '../../ui/Dialog'
import '../../ui/fields.css'
import { AgentSetup } from '../agents/AgentSetup'
import { ConfigSectionPanel, type ConfigSection } from './ConfigSections'
import { GeneralSection } from './GeneralSection'
import { useSettingsForm } from './settingsForm'
import './settings.css'

type Section = 'general' | ConfigSection | 'agents'

const LABELS: Record<Section, Key> = {
  general: 'settings.tab.general',
  servers: 'settings.tab.servers',
  environment: 'settings.tab.environment',
  events: 'settings.tab.events',
  docker: 'settings.tab.docker',
  agents: 'agents.title',
}

const isConfig = (s: Section): s is ConfigSection => s !== 'general' && s !== 'agents'

export function SettingsDialog() {
  const t = useT()
  const local = !useRemote()
  const version = useLive().state?.daemon.version
  const settings = useSettingsForm()
  const sections: Section[] = ['general', 'servers', 'environment', 'events', 'docker', ...(local ? (['agents'] as const) : [])]
  // Opened for the coding agents (the menu bar app's Connect Coding Agents): that section is shown.
  const [toAgents] = useState(() => {
    const d = getUi().dialog
    return d?.kind === 'settings' && d.section === 'agents'
  })
  const [section, setSection] = useState<Section>(toAgents && local ? 'agents' : 'general')

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
    <Dialog title={t('settings.title')} size="lg" className="settings-dialog" onClose={closeDialog} plain>
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
            {section === 'general' && <GeneralSection />}
            {isConfig(section) && <ConfigSectionPanel section={section} settings={settings} />}
            {section === 'agents' && (
              <section className="setting" aria-labelledby="set-agents-title">
                <h3 id="set-agents-title">{t('agents.title')}</h3>
                <p className="setting-hint">{t('agents.body')}</p>
                <AgentSetup reveal={toAgents} />
              </section>
            )}
          </div>
        </DialogMain>
      </div>
      <DialogFoot>
        {version && <span className="foot-note">{t('settings.version', { version })}</span>}
        <span className="foot-spacer" />
        <a className="foot-link" href="/licenses.txt" target="_blank" rel="noreferrer">
          {t('settings.licenses')}
        </a>
        <button type="button" className="btn" onClick={() => openDialog({ kind: 'shortcuts' })}>
          <Keyboard size={16} aria-hidden="true" />
          {t('menu.shortcuts')}
        </button>
        {/* Kept in the layout on every tab (hidden where nothing is saved) so the footer does not move. */}
        <button type="button" className={`btn btn--primary${isConfig(section) ? '' : ' settings-save--hidden'}`} disabled={!settings.dirty || settings.saving} onClick={() => void settings.save()}>
          {settings.saving && <Loader2 size={14} className="spin" aria-hidden="true" />}
          {t('common.save')}
        </button>
      </DialogFoot>
    </Dialog>
  )
}
