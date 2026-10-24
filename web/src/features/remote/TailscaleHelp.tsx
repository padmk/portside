import { ExternalLink } from 'lucide-react'
import type { TailnetProblem } from '../../api/types'
import { useT } from '../../i18n'
import { CopyButton } from '../../ui/CopyButton'
import { isMac } from '../../ui/keys'

const DOWNLOAD = 'https://tailscale.com/download'

function Command({ command }: { command: string }) {
  const t = useT()
  return (
    <div className="cli-line">
      <code>{command}</code>
      <CopyButton text={command} label={t('common.copyCommand', { command })} size={13} />
    </div>
  )
}

/** What to do when Tailscale, which connecting computers needs, is missing, off or signed out. */
export function TailscaleHelp({ problem, detail }: { problem?: TailnetProblem; detail?: string }) {
  const t = useT()
  return (
    <div className="peers-empty" role="status">
      {problem === 'missing' ? (
        <>
          <p>{t('remote.tsMissing')}</p>
          <p className="peers-detail">{t('remote.tsMissingHint')}</p>
          <a className="btn btn--primary" href={DOWNLOAD} target="_blank" rel="noreferrer">
            <ExternalLink size={14} aria-hidden="true" />
            {t('remote.tsDownload')}
          </a>
          {isMac && (
            <>
              <p className="peers-detail">{t('remote.tsBrew')}</p>
              <Command command="brew install --cask tailscale-app" />
            </>
          )}
        </>
      ) : problem === 'stopped' ? (
        <>
          <p>{t('remote.tsStopped')}</p>
          <p className="peers-detail">{isMac ? t('remote.tsStoppedMac') : t('remote.tsStoppedOther')}</p>
          {!isMac && <Command command="sudo tailscale up" />}
        </>
      ) : problem === 'signedOut' ? (
        <>
          <p>{t('remote.tsSignedOut')}</p>
          <p className="peers-detail">{t('remote.tsSignedOutHint')}</p>
          {!isMac && <Command command="sudo tailscale up" />}
        </>
      ) : (
        <>
          <p>{t('remote.noTailnet')}</p>
          {detail && <p className="peers-detail mono">{detail}</p>}
        </>
      )}
    </div>
  )
}
