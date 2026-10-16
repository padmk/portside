// WCAG AA check for every theme the dashboard ships. Run: pnpm check:contrast
// Exits non-zero when a text pairing is below 4.5:1 or a graphic below 3:1.
import { FAMILIES } from '../src/theme/registry.ts'
import { TERMINAL_PALETTES } from '../src/theme/terminal.ts'
import { contrast, loadTheme, over, resolveColor, type Color } from './theme-css.ts'

const THEMES = FAMILIES.flatMap((f) => [
  { id: f.dark, mode: 'dark' as const },
  { id: f.light, mode: 'light' as const },
])

const STATUSES = ['running', 'starting', 'crashed', 'external'] as const

interface Result {
  label: string
  ratio: number
  min: number
}

let failures = 0

for (const { id, mode } of THEMES) {
  const vars = loadTheme(id, mode)
  const c = (name: string) => resolveColor(vars, `var(${name})`)
  const mixed = (expr: string) => resolveColor(vars, expr)
  const solid = (name: string, bg: Color) => over(c(name), bg)

  const bgs: Record<string, Color> = {
    bg: c('--bg'),
    canvas: c('--canvas'),
    card: c('--card'),
    sidebar: c('--sidebar'),
    hover: over(c('--hover'), c('--bg')),
    selected: over(c('--selected'), c('--canvas')),
  }
  const results: Result[] = []
  const check = (label: string, fg: Color, bg: Color, min = 4.5) => results.push({ label, ratio: contrast(fg, bg), min })

  for (const [bn, bg] of Object.entries(bgs)) {
    for (const t of ['--text', '--text-2', '--text-3', '--link', '--stopped-text']) check(`${t} on ${bn}`, solid(t, bg), bg)
    for (const s of STATUSES) check(`--${s}-text on ${bn}`, solid(`--${s}-text`, bg), bg)
  }
  for (const s of [...STATUSES, 'stopped']) {
    for (const bn of ['card', 'canvas', 'sidebar'] as const) {
      const bg = bgs[bn]!
      const tint = over(c(`--${s}-wash`), bg)
      check(`--${s}-text on its wash over ${bn}`, solid(`--${s}-text`, tint), tint)
      check(`--${s} lamp on ${bn}`, solid(`--${s}`, bg), bg, 3)
    }
  }

  const primary = c('--primary')
  check('--primary-fg on --primary', c('--primary-fg'), primary)
  check('--primary-fg on --primary-hover', c('--primary-fg'), over(c('--primary-hover'), bgs.card!))
  for (const bn of ['bg', 'canvas', 'card'] as const) check(`--border-strong on ${bn} (control outline)`, solid('--border-strong', bgs[bn]!), bgs[bn]!, 3)
  for (const bn of ['canvas', 'card', 'sidebar'] as const) check(`--focus ring on ${bn}`, solid('--focus', bgs[bn]!), bgs[bn]!, 3)
  check('--text on --selection', c('--text'), over(c('--selection'), bgs.bg!))
  const soft = over(c('--primary-soft'), bgs.card!)
  check('--text on --primary-soft over card', c('--text'), soft)
  check('--text-2 on --primary-soft over card', solid('--text-2', soft), soft)

  // Another computer is shown: frame, tinted top bar, solid pill, connection card.
  for (const bn of ['canvas', 'sidebar', 'card'] as const) check(`--remote frame on ${bn}`, solid('--remote', bgs[bn]!), bgs[bn]!, 3)
  check('--remote-fg on --remote (pill)', c('--remote-fg'), c('--remote'))
  const topbar = over(c('--remote-wash'), bgs.sidebar!)
  check('--text on remote top bar', c('--text'), topbar)
  for (const s of STATUSES) check(`--${s}-text on remote top bar`, solid(`--${s}-text`, topbar), topbar)
  check('--focus ring on remote top bar', solid('--focus', topbar), topbar, 3)
  const remoteCard = over(c('--remote-wash'), bgs.card!)
  for (const t of ['--text', '--text-2']) check(`${t} on remote wash over card`, solid(t, remoteCard), remoteCard)
  check('--remote icon on remote wash over card', solid('--remote', remoteCard), remoteCard, 3)

  // Pairing: the code card sits on the well inside a dialog (card); an expired code fades to --text-3.
  const pairCard = over(c('--well'), bgs.card!)
  for (const t of ['--text', '--text-2', '--text-3']) check(`${t} on pairing code card (well over card)`, solid(t, pairCard), pairCard)

  for (let n = 1; n <= 6; n++) {
    for (const bn of ['card', 'canvas'] as const) {
      const tile = over(mixed(`color-mix(in srgb, var(--project-${n}) 8%, transparent)`), bgs[bn]!)
      check(`--text on project-${n} tile over ${bn}`, c('--text'), tile)
    }
  }

  const term = TERMINAL_PALETTES[id]
  const tbg = resolveColor({}, term.background)
  check('terminal foreground on background', resolveColor({}, term.foreground), tbg)
  const names = ['black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white']
  const lifted: string[] = []
  term.ansi.forEach((hex, i) => {
    if (i % 8 === 0 || i % 8 === 7) return // black and white rows are background-like by design
    const r = contrast(resolveColor({}, hex), tbg)
    if (r < 4.5) lifted.push(`${i >= 8 ? 'bright ' : ''}${names[i % 8]} ${r.toFixed(1)}`)
  })

  const failed = results.filter((r) => r.ratio < r.min)
  failures += failed.length
  const tightest = results.reduce((a, b) => (b.ratio / b.min < a.ratio / a.min ? b : a))
  console.log(`${failed.length ? 'FAIL' : 'pass'}  ${id.padEnd(22)} ${String(results.length).padStart(3)} pairs, tightest ${tightest.ratio.toFixed(2)} (${tightest.label})`)
  for (const f of failed) console.log(`        below ${f.min}: ${f.label} = ${f.ratio.toFixed(2)}`)
  if (lifted.length) console.log(`        terminal ANSI under 4.5, lifted by minimumContrastRatio: ${lifted.join(', ')}`)
}

console.log(failures ? `\n${failures} failing pairing(s)` : '\nAll interface pairings meet WCAG AA')
process.exit(failures ? 1 : 0)
