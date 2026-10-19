const isMac = /Mac|iPhone|iPad/.test(navigator.platform)

/** How the shortcut sheet and hints write the app's modifier ('mod'). */
export const modLabel = isMac ? '⌘' : 'Ctrl+Shift'

/** The modifier with key as one label, such as ⌘K or Ctrl+Shift+K. */
export const modCombo = (key: string) => (isMac ? modLabel + key : `${modLabel}+${key}`)

/**
 * Whether e is the app's modifier with key: Cmd on macOS, Ctrl+Shift
 * elsewhere, so plain Ctrl keys (Ctrl-F, Ctrl-K) reach shells and REPLs in
 * the terminal.
 */
export function isModKey(e: KeyboardEvent, key: string) {
  if (e.altKey || e.key.toLowerCase() !== key) return false
  return isMac ? e.metaKey && !e.ctrlKey && !e.shiftKey : e.ctrlKey && e.shiftKey && !e.metaKey
}
