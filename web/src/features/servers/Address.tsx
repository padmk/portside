import { Check, Copy, ExternalLink } from 'lucide-react'
import { useState } from 'react'
import type { ServerView } from '../../api/types'
import { useT } from '../../i18n'
import { copyText } from '../../ui/hooks'
import { bareUrl } from './format'

/** A copy button that confirms with a check mark. */
export function CopyButton({ text, label, className = 'icon-btn icon-btn--sm', size = 15 }: { text: string; label: string; className?: string; size?: number }) {
  const t = useT()
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      className={className}
      aria-label={label}
      title={t('common.copy')}
      onClick={async (e) => {
        e.stopPropagation()
        await copyText(text)
        setDone(true)
        window.setTimeout(() => setDone(false), 1200)
      }}
    >
      {done ? <Check size={size} /> : <Copy size={size} />}
    </button>
  )
}

/** The alias reads as a quiet monospace item; the whole item copies it. */
function AliasCopy({ url }: { url: string }) {
  const t = useT()
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      className="addr-alias mono"
      aria-label={t('common.copyUrl', { url })}
      title={t('common.copy')}
      onClick={async (e) => {
        e.stopPropagation()
        await copyText(url)
        setDone(true)
        window.setTimeout(() => setDone(false), 1200)
      }}
    >
      <span>{bareUrl(url)}</span>
      {done ? <Check size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}
    </button>
  )
}

/**
 * The direct address is the large link; the `name.localhost` alias is a quieter
 * item that copies. Links are live only while the server is reachable.
 */
export function Address({ server: v }: { server: ServerView }) {
  const t = useT()
  const reachable = v.status === 'running' || v.status === 'starting'
  if (!v.url) return <p className="addr addr--none mono">{t('detail.noPort')}</p>
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
      {v.alias && <AliasCopy url={v.alias} />}
    </div>
  )
}
