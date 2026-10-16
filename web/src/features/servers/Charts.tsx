import type { ServerView } from '../../api/types'
import { useT } from '../../i18n'
import { useLive } from '../../state/live'
import { AreaChart } from '../../ui/charts'
import { formatBytes, formatCpu, formatSpan } from './format'
import '../../ui/charts.css'

const niceMax = (values: number[], floor: number, step: number) =>
  Math.max(floor, Math.ceil((Math.max(0, ...values) * 1.15) / step) * step)

/** CPU and memory history, labelled with the time the samples really cover. */
export function Charts({ server: v }: { server: Pick<ServerView, 'cpu' | 'mem' | 'cpuHistory' | 'memHistory'> }) {
  const t = useT()
  const sampleMs = useLive().state?.daemon.sampleIntervalMs ?? 2000
  const cpu = v.cpuHistory ?? []
  const mem = v.memHistory ?? []
  const span = (n: number) => (n > 1 ? <span className="span">{t('detail.span', { span: formatSpan(n * sampleMs) })}</span> : null)
  return (
    <section className="charts">
      <figure>
        <figcaption>
          <span>{t('detail.cpu')}</span>
          <strong className="num">{cpu.length ? formatCpu(v.cpu) : '–'}</strong>
          {span(cpu.length)}
        </figcaption>
        <AreaChart values={cpu} max={niceMax(cpu, 10, 10)} format={(x) => `${Math.round(x)}%`} label={t('detail.cpu')} />
      </figure>
      <figure>
        <figcaption>
          <span>{t('detail.memory')}</span>
          <strong className="num">{mem.length ? formatBytes(v.mem) : '–'}</strong>
          {span(mem.length)}
        </figcaption>
        <AreaChart values={mem} max={niceMax(mem, 64 << 20, 64 << 20)} format={formatBytes} label={t('detail.memory')} />
      </figure>
    </section>
  )
}
