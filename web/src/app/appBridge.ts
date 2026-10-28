/**
 * What the macOS app's dashboard window offers the page: its launch-at-login setting and its Dock
 * icon. The window sets `window.appBridge` at document start and handles the `loginItem`,
 * `loginItemProposal` and `appIcon` messages (`null` to `appIcon` asks for the current icon); a browser has neither, so `loginItem` and `appIcon`
 * are null there.
 */
declare global {
  interface Window {
    appBridge?: { loginItem?: unknown; loginItemDecided?: unknown; appIcon?: unknown }
    webkit?: { messageHandlers?: Partial<Record<'loginItem' | 'loginItemProposal' | 'appIcon', { postMessage(body: unknown): Promise<unknown> }>> }
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
  const send = async (handler: { postMessage(body: unknown): Promise<unknown> }, on: boolean) => {
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

export const APP_ICONS = ['light', 'dark'] as const
export type AppIconStyle = (typeof APP_ICONS)[number]

const isIcon = (v: unknown): v is AppIconStyle => APP_ICONS.includes(v as AppIconStyle)

interface AppIcon {
  /** The Dock icon the app shows, as last reported by the app. */
  current(): AppIconStyle
  /** Switches the Dock icon and resolves with the one shown. */
  set(style: AppIconStyle): Promise<AppIconStyle>
  /** Asks the app for the icon it shows now, which its menu may have changed since the page loaded. */
  refresh(): Promise<AppIconStyle>
}

function readIcon(): AppIcon | null {
  const handler = window.webkit?.messageHandlers?.appIcon
  let current = window.appBridge?.appIcon
  if (!isIcon(current) || !handler) return null
  const send = async (body: AppIconStyle | null) => {
    const reply = await handler.postMessage(body)
    if (!isIcon(reply)) throw new Error('unexpected reply from the app')
    current = reply
    return reply
  }
  return {
    current: () => current as AppIconStyle,
    set: (style) => send(style),
    refresh: () => send(null),
  }
}

export const appIcon = readIcon()
