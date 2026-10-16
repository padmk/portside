import { ArrowLeft } from 'lucide-react'
import type { DockerState } from '../../api/types'
import { useT } from '../../i18n'
import { closeServer } from '../../state/route'
import { ContainersView } from './ContainersView'

/** Every container on the engine, with the same actions as on a server's page. */
export function ContainersPage({ docker }: { docker: DockerState }) {
  const t = useT()
  const running = docker.containers.filter((c) => c.state === 'running').length
  return (
    <section className="detail ct-page" aria-label={t('containers.title')}>
      <header className="detail-head">
        <button type="button" className="icon-btn icon-btn--boxed icon-btn--lg" aria-label={t('detail.back')} title={t('detail.backHint')} onClick={closeServer}>
          <ArrowLeft size={19} />
        </button>
        <div className="detail-title">
          <h2>{t('containers.title')}</h2>
          <p className="ct-engine-line">
            {docker.status === 'ready'
              ? [docker.engine, docker.version, docker.context && t('containers.context', { name: docker.context }), t('containers.runningOf', { r: running, n: docker.containers.length })]
                  .filter(Boolean)
                  .join(' · ')
              : docker.engine}
          </p>
        </div>
      </header>
      <ContainersView docker={docker} containers={docker.containers} scope="" />
    </section>
  )
}
