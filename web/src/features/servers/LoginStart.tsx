import { useState } from 'react'
import { Loader2 } from 'lucide-react'
import { loginItem } from '../../app/appBridge'
import { brandTitle } from '../../brand'
import { useT } from '../../i18n'
import { toastError } from '../../state/toasts'

export interface LoginStart {
  on: boolean
  busy: boolean
  toggle(): void
  /** Applies the page's proposal, unless a choice has been made (the app has the last word). */
  confirm(): void
}

/**
 * The launch-at-login choice in the app's window. On the getting-started page it is proposed on
 * until the user has chosen (`propose`), applied when the user starts adding servers or flips the
 * switch; in Settings it always shows the setting as it is;
 * later visits (the page shows whenever there are no servers) show the setting as it is, so a
 * choice made earlier is never undone from here. Null in a browser.
 */
export function useLoginStart({ propose = true }: { propose?: boolean } = {}): LoginStart | null {
  const [actual, setActual] = useState(() => loginItem?.enabled() ?? false)
  const [on, setOn] = useState(() => (propose && !loginItem?.decided() ? true : actual))
  const [busy, setBusy] = useState(false)
  if (!loginItem) return null
  const bridge = loginItem

  const apply = async (next: boolean, proposal: boolean) => {
    setOn(next)
    setBusy(true)
    try {
      const result = await (proposal ? bridge.propose(next) : bridge.set(next))
      setActual(result)
      setOn(result)
    } catch (e) {
      setOn(actual)
      toastError(e)
    } finally {
      setBusy(false)
    }
  }
  return {
    on,
    busy,
    toggle: () => void apply(!on, false),
    // Sent also when it matches the app's setting, so the choice is remembered.
    confirm: () => {
      if (!bridge.decided()) void apply(on, true)
    },
  }
}

/** The switch with its reason; `id` names the reason for the switch, unique per place it is shown. */
export function LoginStartSwitch({ login, id, className }: { login: LoginStart; id: string; className: string }) {
  const t = useT()
  return (
    <div className={className}>
      <button type="button" role="switch" className="switch" aria-checked={login.on} aria-describedby={id} disabled={login.busy} onClick={login.toggle}>
        <span className="track" aria-hidden="true" />
        {t('empty.login', { name: brandTitle })}
        {login.busy && <Loader2 size={14} className="spin" aria-hidden="true" />}
      </button>
      <p id={id}>{t('empty.loginWhy')}</p>
    </div>
  )
}
