import { useEffect, useState } from 'react'
import { FolderOpen } from 'lucide-react'
import { api } from '../../api/client'
import type { ServerView, Spec } from '../../api/types'
import { useT } from '../../i18n'
import { selectServer } from '../../state/route'
import { openOnDesktop } from './actions'
import { CopyButton } from './Address'
import { Charts } from './Charts'

export function DetailsTab({ server: v, servers }: { server: ServerView; servers: ServerView[] }) {
  const t = useT()
  const [spec, setSpec] = useState<Spec | null>(null)
  const sig = `${v.name}|${v.cmd}|${v.stale}`

  useEffect(() => {
    let live = true
    api.spec(v.name).then((s) => live && setSpec(s)).catch(() => live && setSpec(null))
    return () => {
      live = false
    }
  }, [sig])

  const env = Object.entries(spec?.env ?? {})
  const known = new Set(servers.map((s) => s.name))

  return (
    <div className="details">
      <Charts server={v} />

      <dl className="props">
        <dt>{t('detail.command')}</dt>
        <dd className="cmd">
          <code><span aria-hidden="true">$ </span>{v.cmd}</code>
          <CopyButton text={v.cmd} label={t('common.copy')} />
        </dd>

        <dt>{t('detail.directory')}</dt>
        <dd className="dir">
          <code>{v.dirDisplay}</code>
          <button type="button" className="icon-btn icon-btn--sm" aria-label={t('menu.openFolder')} title={t('menu.openFolder')} onClick={() => openOnDesktop('folder', v.name)}>
            <FolderOpen size={16} />
          </button>
        </dd>

        <dt>{t('detail.port')}</dt>
        <dd>{v.port > 0 ? <><code>{v.port}</code>{v.portAuto && <span className="chip">{t('detail.portAuto')}</span>}</> : <span className="muted">{t('detail.noPort')}</span>}</dd>

        <dt>{t('detail.pid')}</dt>
        <dd>{v.pid ? <code>{v.pid}</code> : <span className="muted">–</span>}</dd>

        <dt>{t('detail.dependsOn')}</dt>
        <dd className="deps">
          {v.dependsOn?.length ? (
            v.dependsOn.map((d) =>
              known.has(d) ? (
                <button key={d} type="button" className="chip chip--link" onClick={() => selectServer(d)}>
                  {d}
                </button>
              ) : (
                <span key={d} className="chip">{d}</span>
              ),
            )
          ) : (
            <span className="muted">{t('detail.none')}</span>
          )}
        </dd>

        <dt>{t('detail.restartPolicy')}</dt>
        <dd>{t(`restart.${v.restart === 'on-failure' || v.restart === 'always' ? v.restart : 'no'}`)}</dd>

        {spec?.health && (
          <>
            <dt>{t('detail.health')}</dt>
            <dd><code>{spec.health}</code></dd>
          </>
        )}
        {spec?.stopTimeout && (
          <>
            <dt>{t('detail.stopTimeout')}</dt>
            <dd><code>{spec.stopTimeout}</code></dd>
          </>
        )}

        <dt>{t('detail.env')}</dt>
        <dd>
          {env.length ? (
            <table className="env">
              <tbody>
                {env.map(([k, val]) => (
                  <tr key={k}>
                    <th scope="row">{k}</th>
                    <td>{val}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <span className="muted">{t('detail.none')}</span>
          )}
        </dd>
      </dl>
    </div>
  )
}
