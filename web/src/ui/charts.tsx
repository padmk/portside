import { useId } from 'react'

interface SparkProps {
  values: number[]
  /** Fixed upper bound; defaults to the series maximum. */
  max?: number
  width?: number
  height?: number
  className?: string
  title?: string
}

function points(values: number[], w: number, h: number, max: number, pad = 1.5) {
  const n = values.length
  const step = n > 1 ? w / (n - 1) : w
  return values.map((v, i) => [i * step, h - pad - (Math.min(v, max) / max) * (h - pad * 2)] as const)
}

const line = (p: readonly (readonly [number, number])[]) => p.map(([x, y], i) => `${i ? 'L' : 'M'}${x.toFixed(1)} ${y.toFixed(1)}`).join('')

export function Sparkline({ values, max, width = 64, height = 22, className = '', title }: SparkProps) {
  const top = max ?? Math.max(1, ...values)
  const data = values.length >= 2 ? values : [values[0] ?? 0, values[0] ?? 0]
  const p = points(data, width, height, top)
  return (
    <svg className={`spark ${className}`} width={width} height={height} viewBox={`0 0 ${width} ${height}`} role="img" aria-label={title} focusable="false">
      {title && <title>{title}</title>}
      <path d={line(p)} fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke" />
    </svg>
  )
}

interface AreaProps {
  values: number[]
  max: number
  format: (v: number) => string
  label: string
  height?: number
}

/** Larger area chart with min/max gridlines for the details tab. */
export function AreaChart({ values, max, format, label, height = 84 }: AreaProps) {
  const id = useId()
  const w = 320
  const data = values.length >= 2 ? values : [0, ...values]
  const p = points(data, w, height, max, 2)
  const d = line(p)
  return (
    <div className="area">
      <svg viewBox={`0 0 ${w} ${height}`} preserveAspectRatio="none" role="img" aria-label={label} focusable="false">
        <defs>
          <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" stopColor="currentColor" stopOpacity="0.28" />
            <stop offset="1" stopColor="currentColor" stopOpacity="0" />
          </linearGradient>
        </defs>
        <line x1="0" x2={w} y1="2" y2="2" className="grid" vectorEffect="non-scaling-stroke" />
        <line x1="0" x2={w} y1={height / 2} y2={height / 2} className="grid" vectorEffect="non-scaling-stroke" />
        <line x1="0" x2={w} y1={height - 2} y2={height - 2} className="grid" vectorEffect="non-scaling-stroke" />
        <path d={`${d}L${w} ${height}L0 ${height}Z`} fill={`url(#${id})`} />
        <path d={d} fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
      </svg>
      <span className="area-max">{format(max)}</span>
      <span className="area-min">{format(0)}</span>
    </div>
  )
}
