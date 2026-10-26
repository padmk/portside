import { useId, type CSSProperties } from 'react'
import { createPortal } from 'react-dom'
import { Wifi } from 'lucide-react'
import { useT } from '../../i18n'
import { openDialog } from '../../state/ui'
import type { ServerView } from '../../api/types'
import { serverFix, type FixHint } from './exposure'
import './network.css'

interface Props {
  /** The port the notice is about; 0 for all of a server's listeners. */
  port: number
  fix: FixHint
  /** The server whose `lan = true` silences the notice; none for a container no server owns. */
  server?: string
}

/** Marks a listener reachable from the network, with how to keep it on this computer. */
export function NetworkBadge({ port, fix, server }: Props) {
  const t = useT()
  const id = `net-${useId().replace(/[^a-zA-Z0-9]/g, '')}`
  const anchor = { '--net-anchor': `--${id}` } as CSSProperties
  // The popover lives in body: the badge sits in headings, which take no blocks.
  const hidePopover = () => document.getElementById(id)?.hidePopover()
  const pop = (
    <div id={id} popover="auto" className="net-pop" style={anchor} role="dialog" aria-label={t('network.badge')}>
      <p className="net-pop-title">{port > 0 ? t('network.title', { port }) : t('network.titleServer')}</p>
      <p>{t('network.body')}</p>
      <p>{t(fix.text)}</p>
      {fix.code && <code className="net-code">{fix.code}</code>}
      {server && (
        <>
          <p>{t('network.lan', { name: server })}</p>
          <code className="net-code">lan = true</code>
        </>
      )}
      <p>{t('network.pair')}</p>
      <button type="button" className="btn btn--sm" onClick={() => { hidePopover(); openDialog({ kind: 'computers' }) }}>
        {t('remote.title')}
      </button>
    </div>
  )
  return (
    <>
      <button type="button" className="chip chip--warn net-badge" popoverTarget={id} style={anchor} title={t('network.badgeHint')}>
        <Wifi size={12} aria-hidden="true" />
        {t('network.badge')}
      </button>
      {createPortal(pop, document.body)}
    </>
  )
}

/** The badge for a running server reachable from the network that does not set `lan = true`. */
export function ServerNetworkBadge({ server: v }: { server: ServerView }) {
  if (!v.networkWarning) return null
  return <NetworkBadge port={0} fix={serverFix(v.cmd, v.port)} server={v.temporary ? v.origin : v.name} />
}
