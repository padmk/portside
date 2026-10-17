import type { ServerView } from '../../api/types'
import { useT } from '../../i18n'
import { translateNote } from '../../i18n/notes'
import { usePrefs } from '../../state/prefs'
import { displayStatus } from '../../ui/Lamp'
import { hasProcess } from './format'

/**
 * The line that explains a server's state beyond its status name: the
 * daemon's note, the exit code of a crash, or the computer its port is
 * forwarded from. Empty when the status says it all.
 */
export function useServerNote(v: ServerView): string {
  const t = useT()
  const { locale } = usePrefs()
  const status = displayStatus(v)
  if (v.note && v.note.toLowerCase() !== status) return translateNote(locale, v.note)
  if (status === 'crashed' && v.exitCode !== undefined) return translateNote(locale, `exited with code ${v.exitCode}`)
  if (v.forwardedFrom && !hasProcess(v)) return t('banner.forwardedFrom', { port: v.port, computer: v.forwardedFrom })
  return ''
}

/** A running server with a note is not working as it should, for example not listening on its port. */
export const isDegraded = (v: ServerView, note: string) => v.status === 'running' && note !== ''
