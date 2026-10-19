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
