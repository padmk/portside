import { useEffect, useState } from 'react'
import { api, errorText } from '../../api/client'
import type { ContainerDetails, ContainerView } from '../../api/types'
import { useT } from '../../i18n'
import { selectServer } from '../../state/route'
import { Charts } from '../servers/Charts'
import { CopyButton } from '../servers/Address'
import { EnvTable } from '../servers/EnvTable'

/** What a container runs, how it is wired up, and its resource history. */
export function ContainerDetailsTab({ container: c }: { container: ContainerView }) {
  const t = useT()
  const [d, setD] = useState<ContainerDetails | null>(null)
  const [error, setError] = useState('')
  // Refetch when the container is replaced or changes state (health included).
  const sig = `${c.id}|${c.state}|${c.health ?? ''}`

  useEffect(() => {
    let live = true
    api
      .container(c.name)
      .then((r) => live && (setD(r), setError('')))
      .catch((e) => live && setError(errorText(e) ?? ''))
    return () => {
      live = false
    }
  }, [c.name, sig])

  return (
    <div className="details">
      <dl className="props">
        <dt>{t('container.image')}</dt>
        <dd><code>{c.image}</code></dd>

        {d?.command && (
          <>
            <dt>{t('detail.command')}</dt>
            <dd className="cmd">
              <code>{d.command}</code>
              <CopyButton text={d.command} label={t('common.copy')} />
            </dd>
          </>
        )}

        {c.project && (
          <>
            <dt>{t('container.compose')}</dt>
            <dd>
              <code>{c.project}{c.service ? ` / ${c.service}` : ''}</code>
              {c.server && (
                <button type="button" className="chip chip--link" onClick={() => selectServer(c.server!)}>
                  {c.server}
                </button>
              )}
            </dd>
          </>
        )}
        {d?.configFiles?.length ? (
          <>
            <dt>{t('container.files')}</dt>
            <dd className="ct-stack">
              {d.configFiles.map((f) => <code key={f}>{f}</code>)}
            </dd>
          </>
        ) : null}

        {d?.health && (
          <>
            <dt>{t('container.health')}</dt>
            <dd className="ct-stack">
              <span>
                {t(`container.health.${d.health.status === 'healthy' || d.health.status === 'unhealthy' || d.health.status === 'starting' ? d.health.status : 'none'}`)}
                {d.health.failingStreak > 0 && <span className="muted"> · {t('container.failingStreak', { n: d.health.failingStreak })}</span>}
              </span>
              {d.health.output && <pre className="ct-output">{d.health.output}</pre>}
            </dd>
          </>
        )}
        {d?.error && (
          <>
            <dt>{t('container.error')}</dt>
            <dd><pre className="ct-output">{d.error}</pre></dd>
          </>
        )}

        {d?.restartPolicy && (
          <>
            <dt>{t('detail.restartPolicy')}</dt>
            <dd><code>{d.restartPolicy}</code></dd>
          </>
        )}
        {(c.restarts ?? 0) > 0 && (
          <>
            <dt>{t('detail.restarts')}</dt>
            <dd className="num">{c.restarts}</dd>
          </>
        )}

        <dt>{t('container.ports')}</dt>
        <dd>
          {c.ports?.length ? (
            c.ports.map((p) => (
              <span key={`${p.public}/${p.proto}`} className="chip chip--mono">
                {p.public} → {p.private}/{p.proto}
              </span>
            ))
          ) : (
            <span className="muted">{t('detail.none')}</span>
          )}
        </dd>

        {d && (
          <>
            <dt>{t('container.networks')}</dt>
            <dd className="ct-stack">
              {d.networks.length ? (
                d.networks.map((n) => (
                  <span key={n.name}>
                    <code>{n.name}</code>
                    {n.ip && <span className="muted mono"> {n.ip}</span>}
                    {n.aliases?.length ? <span className="muted"> · {n.aliases.join(', ')}</span> : null}
                  </span>
                ))
              ) : (
                <span className="muted">{t('detail.none')}</span>
              )}
            </dd>

            <dt>{t('container.mounts')}</dt>
            <dd className="ct-stack">
              {d.mounts.length ? (
                d.mounts.map((m) => (
                  <span key={m.destination} className="mono ct-mount">
                    <span>{m.type === 'volume' && m.name ? m.name : m.source}</span>
                    <span className="muted"> → </span>
                    <span>{m.destination}</span>
                    {m.readOnly && <span className="chip">{t('container.readOnly')}</span>}
                  </span>
                ))
              ) : (
                <span className="muted">{t('detail.none')}</span>
              )}
            </dd>

            <dt>{t('detail.env')}</dt>
            <dd>{d.env.length ? <EnvTable env={d.env} /> : <span className="muted">{t('detail.none')}</span>}</dd>
          </>
        )}
        {error && (
          <>
            <dt />
            <dd className="status-text--crashed">{error}</dd>
          </>
        )}
      </dl>
      {c.state === 'running' && <Charts server={c} />}
    </div>
  )
}
