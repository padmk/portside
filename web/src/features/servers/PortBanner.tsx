import { Plug } from 'lucide-react'
import type { ServerView } from '../../api/types'
import { useT } from '../../i18n'
import { startServer, useBusy } from './actions'
import { shortenHome } from './format'
import { useProtectedReason } from './holder'

/**
 * Explains that another process holds the server's port and offers to take it
 * over, or that the port forwards to the connected computer.
 */
export function PortBanner({ server: v }: { server: ServerView }) {
  const t = useT()
  const busy = useBusy(v.name)
  const ext = v.external
  const reason = useProtectedReason(ext?.pid, v.port)

  if (!ext) {
    if (!v.forwardedFrom) return null
    return (
      <div className="banner banner--info" role="status">
        <Plug size={18} aria-hidden="true" />
        <div className="body">
          <strong>{t('banner.forwarded', { port: v.port, computer: v.forwardedFrom })}</strong>
          <p className="banner-sub">{t('banner.forwardedHint')}</p>
        </div>
      </div>
    )
  }
  const vars = { port: v.port, process: ext.command, pid: ext.pid, dir: ext.cwd ? shortenHome(ext.cwd) : '' }
  return (
    <div className="banner banner--info" role="status">
      <Plug size={18} aria-hidden="true" />
      <div className="body">
        <strong>{t(ext.cwd ? 'banner.portUsedIn' : 'banner.portUsed', vars)}</strong>
        {reason && <p className="banner-sub">{t('banner.protected', { reason })}</p>}
      </div>
      {reason === null && (
        <button type="button" className="btn" disabled={busy} onClick={() => startServer(v.name, true)}>
          {t('banner.takeover')}
        </button>
      )}
    </div>
  )
}
