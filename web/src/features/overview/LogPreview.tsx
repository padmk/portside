import { useT } from '../../i18n'

const MARKER = /^──.*──$/
const ERROR = /\b(error|fatal|panic|exception|traceback|failed)\b/i
const WARN = /\bwarn(ing)?\b/i

function kind(line: string): 'marker' | 'error' | 'warn' | undefined {
  if (MARKER.test(line)) return 'marker'
  if (ERROR.test(line)) return 'error'
  if (WARN.test(line)) return 'warn'
  return undefined
}

/** The last few output lines, newest at the bottom, in the terminal's own monospace. */
export function LogPreview({ lines, count, dim }: { lines: string[]; count: number; dim?: boolean }) {
  const t = useT()
  const shown = lines.slice(-count)
  return (
    <div className="log" data-dim={dim || undefined} role="group" aria-label={t('card.recentOutput')} style={{ ['--log-lines' as string]: count }}>
      {shown.length === 0 ? (
        <p className="log-empty">{t('card.noOutput')}</p>
      ) : (
        shown.map((line, i) => (
          <div key={`${i}:${line}`} className="log-line" data-kind={kind(line)}>
            {line}
          </div>
        ))
      )}
    </div>
  )
}
