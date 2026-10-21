import { FolderSearch, Plus } from 'lucide-react'
import { useT } from '../../i18n'
import { useLive } from '../../state/live'
import { openAddServer } from '../../state/ui'
import { Mark } from '../../app/Mark'
import { brand } from '../../brand'
import { CopyButton } from '../../ui/CopyButton'

/** Scanning the current folder, and the configured projects folder unless that is the home folder. */
function commands(root: string | undefined): string[] {
  const out = [`${brand.name} scan --add`]
  if (root && root !== '~') out.push(`${brand.name} scan ${/\s/.test(root) ? JSON.stringify(root) : root} --add`)
  return out
}

export function EmptyState() {
  const t = useT()
  const { state, remote } = useLive()
  // The command line acts on this computer, not on a connected one.
  const local = !remote
  return (
    <div className="empty">
      <Mark className="empty-mark" />
      <h1>{t('empty.title')}</h1>
      <p>{t('empty.body')}</p>
      <div className="empty-actions">
        <button type="button" className="btn btn--primary btn--lg" onClick={() => openAddServer('discover')}>
          <FolderSearch size={15} aria-hidden="true" />
          {t('empty.find')}
        </button>
        <button type="button" className="btn btn--lg" onClick={() => openAddServer('manual')}>
          <Plus size={15} aria-hidden="true" />
          {t('empty.manual')}
        </button>
      </div>
      {local && (
        <div className="empty-cli">
          <p>{t('empty.cli')}</p>
          {commands(state?.config.root).map((c) => (
            <CommandLine key={c} command={c} />
          ))}
        </div>
      )}
    </div>
  )
}

function CommandLine({ command }: { command: string }) {
  const t = useT()
  return (
    <div className="cli-line">
      <code>
        <span aria-hidden="true">$ </span>
        {command}
      </code>
      <CopyButton text={command} label={t('common.copyCommand', { command })} size={13} />
    </div>
  )
}
