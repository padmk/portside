/**
 * Theme catalog. Pure data (no DOM access) so the Vite config can read it to
 * build the pre-paint script in index.html.
 */

export type ThemeMode = 'light' | 'dark'
export type ModeChoice = 'system' | ThemeMode

export const THEME_IDS = [
  'rose-pine',
  'rose-pine-dawn',
  'everforest-dark',
  'everforest-light',
  'tokyo-night',
  'tokyo-night-light',
  'catppuccin-macchiato',
  'catppuccin-latte',
  'nord',
  'nord-light',
  'dark',
  'light',
] as const

export type ThemeId = (typeof THEME_IDS)[number]

export interface ThemeFamily {
  id: string
  label: string
  dark: ThemeId
  light: ThemeId
}

export const FAMILIES: readonly ThemeFamily[] = [
  { id: 'rose-pine', label: 'Rosé Pine', dark: 'rose-pine', light: 'rose-pine-dawn' },
  { id: 'everforest', label: 'Everforest', dark: 'everforest-dark', light: 'everforest-light' },
  { id: 'tokyo-night', label: 'Tokyo Night', dark: 'tokyo-night', light: 'tokyo-night-light' },
  { id: 'catppuccin', label: 'Catppuccin', dark: 'catppuccin-macchiato', light: 'catppuccin-latte' },
  { id: 'nord', label: 'Nord', dark: 'nord', light: 'nord-light' },
  { id: 'neutral', label: 'Neutral', dark: 'dark', light: 'light' },
]

export const DEFAULT_FAMILY = 'rose-pine'
export const DEFAULT_MODE: ModeChoice = 'system'

export const isFamilyId = (v: unknown): v is string => FAMILIES.some((f) => f.id === v)
export const isModeChoice = (v: unknown): v is ModeChoice => v === 'system' || v === 'light' || v === 'dark'

export function familyOf(id: string): ThemeFamily {
  return FAMILIES.find((f) => f.id === id) ?? FAMILIES[0]!
}

export const themeFor = (family: string, mode: ThemeMode): ThemeId => familyOf(family)[mode]
