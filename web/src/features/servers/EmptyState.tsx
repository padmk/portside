import { FolderSearch, Plus } from 'lucide-react'
import { useT } from '../../i18n'
import { useLive } from '../../state/live'
import { openAddServer } from '../../state/ui'
import { Mark } from '../../app/Mark'
import { brand, brandTitle } from '../../brand'
import { CommandLine } from '../../ui/CommandLine'
import { AgentSetup } from '../agents/AgentSetup'
import { LoginStartSwitch, useLoginStart } from './LoginStart'

/** path as one shell word, with a leading ~/ left outside the quotes for the shell to expand. */
function shellWord(path: string): string {
  if (/^[\w./~-]+$/.test(path)) return path
  const home = path.startsWith('~/') ? '~/' : ''
  return `${home}'${path.slice(home.length).replaceAll("'", `'\\''`)}'`
}

/**
 * Scanning the current folder, and the configured projects folder unless that is the home folder
 * or has a backslash, which fish reads as an escape even in single quotes.
 */
function commands(root: string | undefined): string[] {
  const out = [`${brand.name} scan --add`]
  if (root && root !== '~' && !root.includes('\\')) out.push(`${brand.name} scan ${shellWord(root)} --add`)
  return out
}

export function EmptyState() {
  const t = useT()
  const { state, remote } = useLive()
  // The command line acts on this computer, not on a connected one.
  const local = !remote
  // Starting at login applies to this computer, so it is offered only for it.
  const login = useLoginStart()
  return (
    <div className="empty">
      <Mark className="empty-mark" />
      <h1>{t('empty.title')}</h1>
      <p>{t('empty.body')}</p>
      <div className="empty-actions">
        <button type="button" className="btn btn--primary btn--lg" onClick={() => {
            if (local) login?.confirm()
            openAddServer('discover')
          }}>
          <FolderSearch size={15} aria-hidden="true" />
          {t('empty.find')}
        </button>
        <button type="button" className="btn btn--lg" onClick={() => {
            if (local) login?.confirm()
            openAddServer('manual')
          }}>
          <Plus size={15} aria-hidden="true" />
          {t('empty.manual')}
        </button>
      </div>
      {local && login && <LoginStartSwitch login={login} id="empty-login-why" className="empty-login" />}
      {local && (
        <section className="empty-agents" aria-labelledby="empty-agents">
          <h2 id="empty-agents">{t('agents.title')}</h2>
          <p>{t('agents.body')}</p>
          <AgentSetup />
        </section>
      )}
      {local && (
        <div className="empty-cli">
          <p>{t('empty.cli')}</p>
          {commands(state?.config.root).map((c) => (
            <CommandLine key={c} command={c} />
          ))}
          {!login && (
            <>
              <p>{t('empty.service', { name: brandTitle })}</p>
              <CommandLine command={`${brand.name} service install`} />
            </>
          )}
        </div>
      )}
    </div>
  )
}
