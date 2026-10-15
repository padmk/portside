import { AlertTriangle, Info } from 'lucide-react'
import type { ConfigInfo } from '../../api/types'
import { useT } from '../../i18n'
import { openDialog } from '../../state/ui'

export function ConfigBanners({ config }: { config: ConfigInfo }) {
  const t = useT()
  return (
    <>
      {config.error && (
        <div className="banner banner--bad" role="alert">
          <AlertTriangle size={16} aria-hidden="true" />
          <div className="body">
            <strong>{t('config.errorTitle')}</strong> {t('config.errorBody')}
            <pre>{config.error}</pre>
          </div>
          <button type="button" className="btn" onClick={() => openDialog({ kind: 'config' })}>
            {t('menu.editConfig')}
          </button>
        </div>
      )}
      {config.warnings && config.warnings.length > 0 && (
        <div className="banner banner--warn banner--quiet" role="status">
          <Info size={16} aria-hidden="true" />
          <div className="body">
            {config.warnings.map((w) => (
              <p key={w}>{w}</p>
            ))}
          </div>
        </div>
      )}
    </>
  )
}
