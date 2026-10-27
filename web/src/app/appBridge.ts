/**
 * What the macOS app's dashboard window offers the page: its launch-at-login setting. The window
 * sets `window.appBridge` at document start and handles the `loginItem` message; a browser has
 * neither, so `loginItem` is null there.
 */
declare global {
  interface Window {
    appBridge?: { loginItem?: unknown; loginItemDecided?: unknown }
    webkit?: { messageHandlers?: Record<'loginItem' | 'loginItemProposal', { postMessage(body: boolean): Promise<unknown> } | undefined> }
  }
}

interface LoginItem {
  /** Whether the app opens at login, as last reported by the app. */
  enabled(): boolean
  /** Whether the user has turned it on or off before, in the app's menu or here. */
  decided(): boolean
  /** Turns launch at login on or off (the user flipped the switch) and resolves with the resulting state. */
  set(on: boolean): Promise<boolean>
  /**
   * Applies the page's proposal, which the app does only while no choice has been made (in its
   * menu or here); resolves with the resulting state.
   */
  propose(on: boolean): Promise<boolean>
}

function read(): LoginItem | null {
  const bridge = window.appBridge
  const handlers = window.webkit?.messageHandlers
  const toggle = handlers?.loginItem
  const proposal = handlers?.loginItemProposal
  if (typeof bridge?.loginItem !== 'boolean' || !toggle || !proposal) return null
  // The page's copy, kept current after each change, so a later visit to the page shows it.
  let enabled = bridge.loginItem
  let decided = bridge.loginItemDecided === true
  const send = async (handler: { postMessage(body: boolean): Promise<unknown> }, on: boolean) => {
    const reply = await handler.postMessage(on)
    if (typeof reply !== 'boolean') throw new Error('unexpected reply from the app')
    enabled = reply
    decided = true
    return reply
  }
  return {
    enabled: () => enabled,
    decided: () => decided,
    set: (on) => send(toggle, on),
    propose: (on) => send(proposal, on),
  }
}

export const loginItem = read()
