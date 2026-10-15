import type { State } from '../api/types'
import { useT } from '../i18n'
import type { Connection } from '../state/live'
import { Lamp } from '../ui/Lamp'
import { formatBytes, formatCpu, hasProcess } from '../features/servers/format'

export function Footer({ state, connection }: { state: State; connection: Connection }) {
  const t = useT()
  const alive = state.servers.filter(hasProcess)
  const cpu = alive.reduce((n, s) => n + s.cpu, 0)
  const mem = alive.reduce((n, s) => n + s.mem, 0)
  return (
    <footer className="footer">
      <span className="mono">
        <Lamp status={connection === 'open' ? 'running' : 'starting'} decorative /> 127.0.0.1:{state.daemon.port}
      </span>
      <span className="spacer" />
      <span>{t('footer.cpu')} <span className="num">{formatCpu(cpu)}</span></span>
      <span>{t('footer.memory')} <span className="num">{formatBytes(mem)}</span></span>
    </footer>
  )
}
