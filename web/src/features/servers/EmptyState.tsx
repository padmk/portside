import { Check, Copy, FolderSearch, Plus } from 'lucide-react'
import { useState } from 'react'
import { useT } from '../../i18n'
import { useRemote } from '../../state/live'
import { openAddServer } from '../../state/ui'
import { Mark } from '../../app/Mark'
import { brand } from '../../brand'
import { copyText } from '../../ui/hooks'

const COMMANDS = [`${brand.name} scan --add`, `${brand.name} scan ~/projects --add`]

export function EmptyState() {
  const t = useT()
  // The command line acts on this computer, not on a connected one.
  const local = !useRemote()
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
          {COMMANDS.map((c) => (
            <CommandLine key={c} command={c} />
          ))}
        </div>
      )}
    </div>
  )
}

function CommandLine({ command }: { command: string }) {
  const t = useT()
  const [done, setDone] = useState(false)
  return (
    <div className="cli-line">
      <code>
        <span aria-hidden="true">$ </span>
        {command}
      </code>
      <button
        type="button"
        className="icon-btn icon-btn--sm"
        aria-label={t('common.copyCommand', { command })}
        title={t('common.copy')}
        onClick={async () => {
          await copyText(command)
          setDone(true)
          window.setTimeout(() => setDone(false), 1200)
        }}
      >
        {done ? <Check size={13} /> : <Copy size={13} />}
      </button>
    </div>
  )
}
