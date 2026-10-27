// Reads the bundled theme CSS plus semantic.css and resolves custom properties
// to colors. Shared by the Vite config (swatch data) and check-contrast.ts.
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const stylesDir = path.resolve(here, '../src/styles')
const themesDir = path.join(stylesDir, 'themes')

export type Vars = Record<string, string>
export type Color = [number, number, number, number]

const declRe = /(--[\w-]+)\s*:\s*([^;]+);/g

/** Declarations of every rule whose selector matches `test`, in file order. */
function declarations(css: string, test: (selector: string) => boolean): Vars {
  const out: Vars = {}
  const blockRe = /([^{}]+)\{([^{}]*)\}/g
  let b: RegExpExecArray | null
  while ((b = blockRe.exec(css))) {
    if (!test(b[1]!.trim())) continue
    let d: RegExpExecArray | null
    declRe.lastIndex = 0
    while ((d = declRe.exec(b[2]!))) out[d[1]!] = d[2]!.trim()
  }
  return out
}

const stripComments = (css: string) => css.replace(/\/\*[\s\S]*?\*\//g, '')

export function loadTheme(id: string, mode: string): Vars {
  const theme = stripComments(fs.readFileSync(path.join(themesDir, `${id}.css`), 'utf8'))
  const semantic = stripComments(fs.readFileSync(path.join(stylesDir, 'semantic.css'), 'utf8'))
  const base = declarations(semantic, (s) => s === 'html')
  const own = declarations(theme, () => true)
  const modeRules = declarations(semantic, (s) => s === `html[data-mode='${mode}']`)
  const semanticOwn = declarations(semantic, (s) => s === `html[data-theme='${id}']`)
  // Same order as the cascade: base, bundled palette, mode defaults, per-theme semantic.
  return { ...base, ...own, ...modeRules, ...semanticOwn }
}

const parseHex = (h: string): Color => {
  let s = h.slice(1)
  if (s.length === 3) s = [...s].map((c) => c + c).join('')
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(s.slice(i, i + 2), 16))
  return [r!, g!, b!, s.length === 8 ? parseInt(s.slice(6, 8), 16) / 255 : 1]
}

function splitTop(s: string): string[] {
  const parts: string[] = []
  let depth = 0
  let cur = ''
  for (const ch of s) {
    if (ch === '(') depth++
    if (ch === ')') depth--
    if (ch === ',' && depth === 0) {
      parts.push(cur.trim())
      cur = ''
    } else cur += ch
  }
  parts.push(cur.trim())
  return parts
}

/** Returns [r, g, b, a] with r,g,b in 0..255. */
export function resolveColor(vars: Vars, value: string, depth = 0): Color {
  if (depth > 20) throw new Error(`cycle resolving ${value}`)
  const v = value.trim()
  if (v === 'transparent') return [0, 0, 0, 0]
  if (v.startsWith('#')) return parseHex(v)
  const ref = /^var\((--[\w-]+)(?:\s*,\s*(.+))?\)$/.exec(v)
  if (ref) {
    const next = vars[ref[1]!] ?? ref[2]
    if (next === undefined) throw new Error(`unresolved ${ref[1]}`)
    return resolveColor(vars, next, depth + 1)
  }
  const rgb = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)(?:\s*[,/]\s*([\d.]+%?))?\s*\)$/.exec(v)
  if (rgb) {
    const a = rgb[4] === undefined ? 1 : rgb[4].endsWith('%') ? parseFloat(rgb[4]) / 100 : parseFloat(rgb[4])
    return [+rgb[1]!, +rgb[2]!, +rgb[3]!, a]
  }
  const mix = /^color-mix\(\s*in srgb\s*,(.*)\)$/.exec(v)
  if (mix) {
    const [a, b] = splitTop(mix[1]!) as [string, string]
    const parse = (s: string) => {
      const m = /^(.*?)(?:\s+([\d.]+)%)?$/.exec(s.trim())!
      return { color: resolveColor(vars, m[1]!, depth + 1), pct: m[2] === undefined ? undefined : parseFloat(m[2]) }
    }
    const x = parse(a)
    const y = parse(b)
    let p1 = x.pct
    let p2 = y.pct
    if (p1 === undefined && p2 === undefined) p1 = p2 = 50
    else if (p1 === undefined) p1 = 100 - p2!
    else if (p2 === undefined) p2 = 100 - p1
    p1 = p1!
    p2 = p2!
    const sum = p1 + p2
    const w1 = p1 / sum
    const w2 = p2 / sum
    const alpha = (x.color[3] * w1 + y.color[3] * w2) * (sum < 100 ? sum / 100 : 1)
    const ch = (i: 0 | 1 | 2) => {
      const num = x.color[i] * x.color[3] * w1 + y.color[i] * y.color[3] * w2
      const den = x.color[3] * w1 + y.color[3] * w2
      return den === 0 ? 0 : num / den
    }
    return [ch(0), ch(1), ch(2), alpha]
  }
  throw new Error(`unsupported color: ${v}`)
}

/** Composites a possibly translucent color over an opaque background. */
export function over(fg: Color, bg: Color): Color {
  const a = fg[3]
  const ch = (i: 0 | 1 | 2) => fg[i] * a + bg[i] * (1 - a)
  return [ch(0), ch(1), ch(2), 1]
}

const channel = (v: number) => {
  v /= 255
  return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4
}
const luminance = (c: Color) => 0.2126 * channel(c[0]) + 0.7152 * channel(c[1]) + 0.0722 * channel(c[2])
export function contrast(a: Color, b: Color): number {
  const x = luminance(a)
  const y = luminance(b)
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05)
}

export const toHex = (c: Color) => '#' + [0, 1, 2].map((i) => Math.round(c[i]!).toString(16).padStart(2, '0')).join('')
