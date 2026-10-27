import { useT } from '../i18n'
import { CopyButton } from './CopyButton'

/** A shell command to copy, shown with a prompt. */
export function CommandLine({ command }: { command: string }) {
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
