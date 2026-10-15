import type { Locale } from '../state/prefs'

type Rule = [RegExp, (m: RegExpMatchArray) => string]

/** Polish renderings of the daemon's status notes, which arrive in English. */
const pl: Rule[] = [
  [/^waiting for (.+)$/, (m) => `czeka na ${m[1]}`],
  [/^exited with code (-?\d+)$/, (m) => `zakończył się z kodem ${m[1]}`],
  [/^exited on its own$/, () => 'zakończył się sam'],
  [/^port (\d+) is used by (.+) \(pid (\d+)\)$/, (m) => `port ${m[1]} zajmuje ${m[2]} (pid ${m[3]})`],
  [/^port (\d+) is already in use$/, (m) => `port ${m[1]} jest już zajęty`],
  [/^directory not found: (.+)$/, (m) => `brak katalogu: ${m[1]}`],
  [/^failed to start: (.+)$/, (m) => `nie udało się uruchomić: ${m[1]}`],
  [/^dependency (\S+) failed: (.+)$/, (m) => `zależność ${m[1]} nie wystartowała: ${m[2]}`],
  [/^dependency (\S+) did not become ready: (.+)$/, (m) => `zależność ${m[1]} nie była gotowa: ${m[2]}`],
]

const plSuffixes: Rule[] = [
  [/, restarting in (\S+)$/, (m) => `, restart za ${m[1]}`],
  [/; gave up after (\d+) restarts$/, (m) => `; bez kolejnych prób (restarty: ${m[1]})`],
]

export function translateNote(locale: Locale, note: string): string {
  if (locale !== 'pl' || !note) return note
  let suffix = ''
  let base = note
  for (const [re, fn] of plSuffixes) {
    const m = base.match(re)
    if (m && m.index !== undefined) {
      suffix = fn(m) + suffix
      base = base.slice(0, m.index)
    }
  }
  for (const [re, fn] of pl) {
    const m = base.match(re)
    if (m) return fn(m) + suffix
  }
  return note
}
