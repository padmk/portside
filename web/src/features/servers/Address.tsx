import { ExternalLink } from 'lucide-react'
import type { ServerView } from '../../api/types'
import { useT } from '../../i18n'
import { CopyButton } from '../../ui/CopyButton'
import { bareUrl, isUp } from './format'

/**
 * The direct address is the large link; the `name.localhost` alias is a quieter
 * item that copies. Links are live only while the server is reachable.
 */
export function Address({ server: v }: { server: ServerView }) {
  const t = useT()
  const reachable = isUp(v)
  if (!v.url) {
    return (
      <div className="addr">
        <p className="addr--none mono">{t('detail.noPort')}</p>
        <ListenPorts server={v} />
      </div>
    )
  }
  return (
    <div className="addr">
      {reachable ? (
        <a className="addr-main mono" href={v.url} target="_blank" rel="noreferrer" title={t('common.openUrl', { url: v.url })}>
          {bareUrl(v.url)}
          <ExternalLink size={16} aria-hidden="true" />
        </a>
      ) : (
        <span className="addr-main addr-main--off mono">{bareUrl(v.url)}</span>
      )}
      {/* The alias reads as a quiet monospace item; the whole item copies it. */}
      {v.alias && (
        <CopyButton text={v.alias} label={t('common.copyUrl', { url: v.alias })} className="addr-alias mono" size={14}>
          <span>{bareUrl(v.alias)}</span>
        </CopyButton>
      )}
      <ListenPorts server={v} />
    </div>
  )
}

/** The ports the process listens on when they are not the configured one. */
function ListenPorts({ server: v }: { server: ServerView }) {
  const t = useT()
  if (!v.listenPorts?.length) return null
  return (
    <span className="chip chip--warn mono" title={t(v.port > 0 ? 'detail.listensOnHint' : 'detail.listensOnNoPort', { port: v.port })}>
      {t('detail.listensOn', { ports: v.listenPorts.join(', ') })}
    </span>
  )
}
