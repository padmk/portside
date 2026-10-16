import { Loader2, Play } from 'lucide-react'
import type { DockerState } from '../../api/types'
import { useT } from '../../i18n'
import { translateNote } from '../../i18n/notes'
import { usePrefs } from '../../state/prefs'
import { useBusy } from '../servers/actions'
import { startEngine } from './actions'

/** Shown where containers would be while the engine is down: why, and a way to start it. */
export function EngineNotice({ docker, compact }: { docker: DockerState; compact?: boolean }) {
  const t = useT()
  const { locale } = usePrefs()
  const busy = useBusy('docker:start')
  if (docker.status === 'ready') return null
  const starting = docker.starting || busy
  const connecting = docker.status === 'connecting'
  const reason = docker.error && docker.error !== 'Docker is not running' ? translateNote(locale, docker.error) : ''
  return (
    <div className={compact ? 'banner banner--warn' : 'ct-engine'} role="status">
      <div className="body">
        <strong>{connecting ? t('docker.connecting') : starting ? t('docker.startingTitle') : t('docker.down')}</strong>{' '}
        {!connecting && !starting && <span>{t('docker.downBody', { engine: docker.engine })}</span>}
        {reason && !starting && <span className="ct-engine-reason">{reason}</span>}
      </div>
      {!connecting && (
        <button type="button" className={compact ? 'btn' : 'btn btn--primary btn--lg'} disabled={starting} onClick={() => void startEngine()}>
          {starting ? <Loader2 size={16} className="spin" aria-hidden="true" /> : <Play size={15} fill="currentColor" aria-hidden="true" />}
          {starting ? t('docker.startingButton') : t('docker.start')}
        </button>
      )}
    </div>
  )
}
