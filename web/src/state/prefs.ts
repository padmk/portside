import { useSyncExternalStore } from 'react'
import { storageKey } from '../brand'
import {
  DEFAULT_FAMILY,
  DEFAULT_MODE,
  isFamilyId,
  isModeChoice,
  themeFor,
  type ModeChoice,
  type ThemeId,
  type ThemeMode,
} from '../theme/registry'

export type Locale = 'en' | 'pl'

const FAMILY_KEY = storageKey('themeFamily')
const MODE_KEY = storageKey('themeMode')
/** Earlier versions stored only 'light' or 'dark' under this key. */
const LEGACY_MODE_KEY = storageKey('theme')
const LOCALE_KEY = storageKey('locale')

function read(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

export function write(key: string, value: string | null) {
  try {
    if (value === null) localStorage.removeItem(key)
    else localStorage.setItem(key, value)
  } catch {
    /* storage unavailable */
  }
}

const systemDark = window.matchMedia('(prefers-color-scheme: dark)')

function initialFamily(): string {
  const v = read(FAMILY_KEY)
  return isFamilyId(v) ? v : DEFAULT_FAMILY
}

function initialMode(): ModeChoice {
  const v = read(MODE_KEY) ?? read(LEGACY_MODE_KEY)
  return isModeChoice(v) ? v : DEFAULT_MODE
}

function initialLocale(): Locale {
  const v = read(LOCALE_KEY)
  if (v === 'en' || v === 'pl') return v
  return navigator.language.toLowerCase().startsWith('pl') ? 'pl' : 'en'
}

interface Prefs {
  family: string
  modeChoice: ModeChoice
  /** The mode in effect: the explicit choice, or the system setting. */
  mode: ThemeMode
  themeId: ThemeId
  locale: Locale
}

const resolveMode = (c: ModeChoice): ThemeMode => (c === 'system' ? (systemDark.matches ? 'dark' : 'light') : c)

function build(family: string, modeChoice: ModeChoice, locale: Locale): Prefs {
  const mode = resolveMode(modeChoice)
  return { family, modeChoice, mode, themeId: themeFor(family, mode), locale }
}

let snapshot = build(initialFamily(), initialMode(), initialLocale())
const listeners = new Set<() => void>()

function apply() {
  const root = document.documentElement
  root.dataset.theme = snapshot.themeId
  root.dataset.mode = snapshot.mode
  root.style.colorScheme = snapshot.mode
  root.lang = snapshot.locale
}

function publish(next: Prefs) {
  snapshot = next
  apply()
  listeners.forEach((l) => l())
}

apply()
systemDark.addEventListener('change', () => publish(build(snapshot.family, snapshot.modeChoice, snapshot.locale)))

export function setThemeFamily(family: string) {
  if (!isFamilyId(family)) return
  write(FAMILY_KEY, family)
  publish(build(family, snapshot.modeChoice, snapshot.locale))
}

export function setThemeMode(mode: ModeChoice) {
  write(MODE_KEY, mode)
  publish(build(snapshot.family, mode, snapshot.locale))
}

/** Flips between light and dark within the current family. */
export const toggleThemeMode = () => setThemeMode(snapshot.mode === 'dark' ? 'light' : 'dark')

export function setLocale(locale: Locale) {
  write(LOCALE_KEY, locale)
  publish(build(snapshot.family, snapshot.modeChoice, locale))
}

export function usePrefs(): Prefs {
  return useSyncExternalStore(
    (cb) => (listeners.add(cb), () => listeners.delete(cb)),
    () => snapshot,
  )
}

export const getLocale = () => snapshot.locale
