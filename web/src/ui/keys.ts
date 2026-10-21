import type { KeyboardEvent as ReactKeyboardEvent } from 'react'
import type { Translate } from '../i18n'

const isMac = /Mac|iPhone|iPad/.test(navigator.platform)

/** How the shortcut sheet and hints write the app's modifier ('mod'). */
export const modLabel = isMac ? '⌘' : 'Ctrl+Shift'

/** How the shortcut sheet and hints write a key: 'mod' and 'Space' are translated. */
export const keyLabel = (key: string, t: Translate) => (key === 'mod' ? modLabel : key === 'Space' ? t('keys.space') : key)

/** The modifier with key as one label, such as ⌘K or Ctrl+Shift+Space. */
export const modCombo = (key: string, t: Translate) => (isMac ? modLabel + keyLabel(key, t) : `${modLabel}+${keyLabel(key, t)}`)

/**
 * Whether e is the app's modifier with key: Cmd on macOS, Ctrl+Shift
 * elsewhere, so plain Ctrl keys (Ctrl-F, Ctrl-K) reach shells and REPLs in
 * the terminal.
 */
export function isModKey(e: KeyboardEvent, key: string) {
  if (e.altKey || e.key.toLowerCase() !== key) return false
  return isMac ? e.metaKey && !e.ctrlKey && !e.shiftKey : e.ctrlKey && e.shiftKey && !e.metaKey
}

/**
 * The key that opens the command palette with the modifier: ⌘K on macOS,
 * Ctrl+Shift+Space elsewhere. On Linux and Windows the usual choices are
 * taken: Chrome, Edge and Firefox do not reserve Ctrl+Shift+Space, while
 * Ctrl+Shift+K opens Firefox's Web Console, Ctrl+Shift+P a private window in
 * Firefox, F1 Chrome's help, and Ctrl+/ is undo for readline and Emacs in the
 * terminal.
 */
export const paletteKey = isMac ? 'K' : 'Space'

/** Whether e opens the command palette. */
export function isPaletteKey(e: KeyboardEvent) {
  if (isMac) return isModKey(e, 'k')
  return e.code === 'Space' && e.ctrlKey && e.shiftKey && !e.altKey && !e.metaKey
}

/**
 * Arrow-key handling for a tab of a tablist: Left and Right select and focus
 * the neighbouring tab, wrapping around; the tab buttons have the id
 * `${idPrefix}${tab}`. The keys are handled here, so the global shortcuts do
 * not also switch servers.
 */
export function onTabKey<T extends string>(e: ReactKeyboardEvent, tabs: readonly T[], current: T, select: (tab: T) => void, idPrefix: string) {
  if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return
  e.preventDefault()
  e.stopPropagation()
  const i = tabs.indexOf(current) + (e.key === 'ArrowRight' ? 1 : -1)
  const next = tabs[(i + tabs.length) % tabs.length]!
  select(next)
  requestAnimationFrame(() => document.getElementById(idPrefix + next)?.focus())
}
