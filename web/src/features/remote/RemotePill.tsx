import { Loader2, MonitorSmartphone, Unplug } from 'lucide-react'
import type { RemoteConnection } from '../../api/types'
import { useT } from '../../i18n'
import { openDialog } from '../../state/ui'
import { useBusy } from '../servers/actions'
import { disconnectRemote } from './actions'
import { OsIcon } from './ComputersDialog'
import './remote.css'

const openComputers = () => openDialog({ kind: 'computers' })

/** Opens the computers dialog from this computer's top bar. */
export function ComputersButton() {
  const t = useT()
  return (
    <button type="button" className="icon-btn icon-btn--boxed" aria-label={t('remote.title')} title={t('remote.title')} onClick={openComputers}>
      <MonitorSmartphone size={18} />
    </button>
  )
}

/** Names the remote computer the dashboard shows, with its connection state and a way back. */
export function RemotePill({ remote }: { remote: RemoteConnection }) {
  const t = useT()
  const busy = useBusy('remote:disconnect')
  const name = remote.peer.name
  const pending = remote.status !== 'open'
  return (
    <div className={`remote-pill${pending ? ' remote-pill--pending' : ''}`}>
      <button type="button" className="remote-pill-main" aria-label={t('remote.pill', { name })} title={remote.peer.dnsName} onClick={openComputers}>
        <OsIcon os={remote.peer.os} size={16} />
        <span className="remote-pill-name">{name}</span>
        {pending ? (
          <span className="remote-pill-state" role="status">
            <span className="remote-lamp" aria-hidden="true" />
            {t(remote.status === 'connecting' ? 'top.connecting' : 'top.reconnecting')}
          </span>
        ) : (
          <>
            <span className="remote-pill-os">{remote.peer.os}</span>
            <span className="remote-lamp" aria-hidden="true" />
          </>
        )}
      </button>
      <button
        type="button"
        className="remote-pill-end"
        disabled={busy}
        aria-label={t('remote.disconnectFrom', { name })}
        title={t('remote.disconnectFrom', { name })}
        onClick={() => void disconnectRemote()}
      >
        {busy ? <Loader2 size={15} className="spin" /> : <Unplug size={15} />}
      </button>
    </div>
  )
}
