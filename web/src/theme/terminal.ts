import type { ITheme } from '@xterm/xterm'
import type { ThemeId, ThemeMode } from './registry'

interface Palette {
  background: string
  foreground: string
  cursor: string
  selection: string
  /** ANSI colors 0 to 15: black, red, green, yellow, blue, magenta, cyan, white, then the bright row. */
  ansi: readonly [string, string, string, string, string, string, string, string, string, string, string, string, string, string, string, string]
}

/** Terminal colors per theme, from each family's own terminal palette. */
const palettes: Record<ThemeId, Palette> = {
  // Rose Pine Moon
  'rose-pine': {
    background: '#232136', foreground: '#e0def4', cursor: '#e0def4', selection: '#44415a',
    ansi: ['#393552', '#eb6f92', '#3e8fb0', '#f6c177', '#9ccfd8', '#c4a7e7', '#ea9a97', '#e0def4',
      '#6e6a86', '#eb6f92', '#3e8fb0', '#f6c177', '#9ccfd8', '#c4a7e7', '#ea9a97', '#e0def4'],
  },
  'rose-pine-dawn': {
    background: '#faf4ed', foreground: '#575279', cursor: '#575279', selection: '#dfdad9',
    ansi: ['#f2e9e1', '#b4637a', '#286983', '#ea9d34', '#56949f', '#907aa9', '#d7827e', '#575279',
      '#9893a5', '#b4637a', '#286983', '#ea9d34', '#56949f', '#907aa9', '#d7827e', '#575279'],
  },
  // Everforest (sainnhe)
  'everforest-dark': {
    background: '#1e2326', foreground: '#d3c6aa', cursor: '#d3c6aa', selection: '#445257',
    ansi: ['#374247', '#e67e80', '#a7c080', '#dbbc7f', '#7fbbb3', '#d699b6', '#83c092', '#d3c6aa',
      '#859289', '#e67e80', '#a7c080', '#dbbc7f', '#7fbbb3', '#d699b6', '#83c092', '#d3c6aa'],
  },
  'everforest-light': {
    background: '#fffbef', foreground: '#5c6a72', cursor: '#5c6a72', selection: '#e4e3d9',
    ansi: ['#5c6a72', '#f85552', '#8da101', '#dfa000', '#3a94c5', '#df69ba', '#35a77c', '#e0dcc7',
      '#829181', '#f85552', '#8da101', '#dfa000', '#3a94c5', '#df69ba', '#35a77c', '#bdc3af'],
  },
  // Tokyo Night (Storm) and Tokyo Night Light
  'tokyo-night': {
    background: '#24283b', foreground: '#c0caf5', cursor: '#c0caf5', selection: '#364a82',
    ansi: ['#1d202f', '#f7768e', '#9ece6a', '#e0af68', '#7aa2f7', '#bb9af7', '#7dcfff', '#a9b1d6',
      '#414868', '#f7768e', '#9ece6a', '#e0af68', '#7aa2f7', '#bb9af7', '#7dcfff', '#c0caf5'],
  },
  'tokyo-night-light': {
    background: '#eaeaed', foreground: '#343b58', cursor: '#343b58', selection: '#d2dfe8',
    ansi: ['#0f0f14', '#8c4351', '#485e30', '#8f5e15', '#34548a', '#5a4a78', '#0f4b6e', '#343b58',
      '#9699a3', '#8c4351', '#485e30', '#8f5e15', '#34548a', '#5a4a78', '#0f4b6e', '#343b58'],
  },
  // Catppuccin Macchiato and Latte
  'catppuccin-macchiato': {
    background: '#24273a', foreground: '#cad3f5', cursor: '#f4dbd6', selection: '#494d64',
    ansi: ['#494d64', '#ed8796', '#a6da95', '#eed49f', '#8aadf4', '#f5bde6', '#8bd5ca', '#b8c0e0',
      '#5b6078', '#ed8796', '#a6da95', '#eed49f', '#8aadf4', '#f5bde6', '#8bd5ca', '#a5adcb'],
  },
  'catppuccin-latte': {
    background: '#eff1f5', foreground: '#4c4f69', cursor: '#dc8a78', selection: '#ccd0da',
    ansi: ['#5c5f77', '#d20f39', '#40a02b', '#df8e1d', '#1e66f5', '#ea76cb', '#179299', '#acb0be',
      '#6c6f85', '#de293e', '#49af3d', '#eea02d', '#456eff', '#fe85d8', '#2d9fa8', '#bcc0cc'],
  },
  // Nord. The light variant keeps Nord's hues, darkened to stay legible on white.
  nord: {
    background: '#2f3440', foreground: '#d8dee9', cursor: '#d8dee9', selection: '#434c5e',
    ansi: ['#3b4252', '#bf616a', '#a3be8c', '#ebcb8b', '#81a1c1', '#b48ead', '#88c0d0', '#e5e9f0',
      '#4c566a', '#bf616a', '#a3be8c', '#ebcb8b', '#81a1c1', '#b48ead', '#8fbcbb', '#eceff4'],
  },
  'nord-light': {
    background: '#ffffff', foreground: '#2e3440', cursor: '#2e3440', selection: '#d8dee9',
    ansi: ['#3b4252', '#b0525b', '#5d7f47', '#8c6d1f', '#4c6f9c', '#8a6384', '#35788a', '#6b7487',
      '#4c566a', '#bf616a', '#6b8a55', '#9a7a2c', '#5e81ac', '#9b7096', '#3f8a9c', '#4c566a'],
  },
  // Neutral: Portside's own palette
  dark: {
    background: '#1b1b1d', foreground: '#ededee', cursor: '#ededee', selection: '#33507a',
    ansi: ['#3b3b40', '#ff6b72', '#79b895', '#d5ac62', '#6fa9f0', '#c792ea', '#5fc8c8', '#d5d0c6',
      '#5b6772', '#ff8e93', '#9bd1b0', '#e6c47e', '#93c5f8', '#dcaff0', '#86dada', '#f4f2ed'],
  },
  light: {
    background: '#ffffff', foreground: '#232326', cursor: '#232326', selection: '#c9dcf5',
    ansi: ['#171d23', '#c42b33', '#2a7549', '#85590a', '#245ca0', '#8e3fa8', '#17757a', '#59636d',
      '#66707a', '#d94a52', '#388f5b', '#a5741a', '#3a78c2', '#a857c2', '#258a90', '#38424b'],
  },
}

const names = ['black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white'] as const

function toTheme(p: Palette): ITheme {
  const theme: ITheme = {
    background: p.background,
    foreground: p.foreground,
    cursor: p.cursor,
    cursorAccent: p.background,
    selectionBackground: p.selection,
  }
  const out = theme as Record<string, string>
  names.forEach((n, i) => {
    out[n] = p.ansi[i]!
    out[`bright${n[0]!.toUpperCase()}${n.slice(1)}`] = p.ansi[i + 8]!
  })
  return theme
}

export const terminalTheme = (id: ThemeId): ITheme => toTheme(palettes[id])

/** Colors the terminal pane uses around xterm itself. */
export const terminalBackground = (id: ThemeId) => palettes[id].background

/** Search highlights need opaque #rrggbb values. */
export function searchDecorations(mode: ThemeMode) {
  const dark = mode === 'dark'
  return {
    matchBackground: dark ? '#5a4a22' : '#f2dca6',
    matchBorder: dark ? '#8a7236' : '#d6b25e',
    matchOverviewRuler: dark ? '#d5ac62' : '#b7821a',
    activeMatchBackground: dark ? '#9a6b1c' : '#f0b84a',
    activeMatchBorder: dark ? '#e3be78' : '#85590a',
    activeMatchColorOverviewRuler: dark ? '#e06a55' : '#bf4b38',
  }
}

/** Raw palettes for tooling that checks contrast. */
export const TERMINAL_PALETTES = palettes
