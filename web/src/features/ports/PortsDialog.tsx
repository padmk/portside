import { useCallback, useEffect, useState } from 'react'
import { Loader2, RefreshCw, OctagonX } from 'lucide-react'
import { api } from '../../api/client'
import type { ListenerView } from '../../api/types'
import { useT } from '../../i18n'
import { closeDialog } from '../../state/ui'
import { selectServer } from '../../state/route'
import { toast, toastError } from '../../state/toasts'
import { Dialog } from '../../ui/Dialog'
import { ConfirmDialog } from '../dialogs/ConfirmDialog'
import { showContainer } from '../containers/ContainerStrip'
import { pickContainer } from '../../state/ui'
import { openContainers } from '../../state/route'
import './ports.css'

export function PortsDialog() {
  const t = useT()
  const [rows, setRows] = useState<ListenerView[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [kill, setKill] = useState<ListenerView | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setRows((await api.listeners()).listeners)
    } catch (e) {
      toastError(e)
      setRows((r) => r ?? [])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <Dialog
      title={t('ports.title')}
      size="lg"
      onClose={closeDialog}
      footer={
        <>
          <span className="foot-note">{rows ? t('ports.count', { n: rows.length }) : ''}</span>
          <span className="foot-spacer" />
          <button type="button" className="btn" disabled={loading} onClick={load}>
            <RefreshCw size={14} className={loading ? 'spin' : undefined} aria-hidden="true" />
            {t('common.refresh')}
          </button>
        </>
      }
    >
      {rows === null ? (
        <p className="scan-state"><Loader2 size={15} className="spin" aria-hidden="true" /> {t('common.loading')}</p>
      ) : rows.length === 0 ? (
        <p className="empty-note">{t('ports.none')}</p>
      ) : (
        <table className="ports">
          <thead>
            <tr>
              <th scope="col">{t('ports.port')}</th>
              <th scope="col">{t('ports.process')}</th>
              <th scope="col">{t('ports.pid')}</th>
              <th scope="col">{t('ports.cwd')}</th>
              <th scope="col">{t('ports.owner')}</th>
              <th scope="col"><span className="sr-only">{t('ports.kill')}</span></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={`${r.pid}:${r.port}`}>
                <td><span className="chip chip--mono">{r.port}</span></td>
                <td className="proc">{r.command}</td>
                <td className="num">{r.pid}</td>
                <td className="cwd mono" title={r.cwd}>{r.cwdDisplay || '–'}</td>
                <td>
                  <Owner row={r} />
                </td>
                <td className="kill">
                  <span title={r.protected ? t('ports.protected', { reason: r.protected }) : undefined}>
                    <button
                      type="button"
                      className="btn btn--danger"
                      disabled={!!r.protected}
                      aria-label={t('ports.killNamed', { process: r.command, port: r.port })}
                      onClick={() => setKill(r)}
                    >
                      <OctagonX size={13} aria-hidden="true" />
                      {t('ports.kill')}
                    </button>
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {kill && (
        <ConfirmDialog
          title={t('ports.confirmTitle', { port: kill.port })}
          message={t('ports.confirmBody', { process: kill.command, pid: kill.pid, port: kill.port })}
          confirmLabel={t('ports.kill')}
          onClose={() => setKill(null)}
          onConfirm={async () => {
            await api.killListener(kill.pid, kill.port)
            toast(t('ports.killed', { process: kill.command, port: kill.port }))
            await load()
          }}
        />
      )}
    </Dialog>
  )
}

function Owner({ row }: { row: ListenerView }) {
  const t = useT()
  const link = (name: string) => (
    <button
      type="button"
      className="link"
      onClick={() => {
        selectServer(name)
        closeDialog()
      }}
    >
      {name}
    </button>
  )
  const ct = row.container
  if (ct)
    return (
      <span className="chip chip--info">
        {t('ports.container')}{' '}
        <button
          type="button"
          className="link"
          onClick={() => {
            closeDialog()
            if (ct.server) showContainer(ct.server, ct.name)
            else {
              pickContainer('', ct.name)
              openContainers()
            }
          }}
        >
          {ct.service ?? ct.name}
        </button>
        {ct.server && <> ({link(ct.server)})</>}
      </span>
    )
  if (row.managed && row.server) return <span className="chip chip--ok">{t('ports.managed')} {link(row.server)}</span>
  if (row.server) return <span className="chip chip--warn">{t('ports.blocks')} {link(row.server)}</span>
  return <span className="chip">{t('ports.unmanaged')}</span>
}
