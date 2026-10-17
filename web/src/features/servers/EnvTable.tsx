import { useState } from 'react'
import { Eye, EyeOff } from 'lucide-react'
import { useT } from '../../i18n'

/** Variable names whose values are hidden until revealed. */
const SECRET = /(pass(word)?|passwd|secret|token|api[_-]?key|private|credential|auth|cookie|session|signature|salt|dsn|access[_-]?key|(^|_)key$)/i

/** URLs that carry a password, such as postgres://user:pass@host/db. */
const URL_PASSWORD = /^[a-z][a-z0-9+.-]*:\/\/[^/@:\s]+:[^/@\s]+@/i

const isSecret = (key: string, value: string) => SECRET.test(key) || URL_PASSWORD.test(value)

/** Environment variables of a server or container, secret-looking values hidden until revealed. */
export function EnvTable({ env }: { env: { key: string; value: string }[] }) {
  const t = useT()
  const [shown, setShown] = useState<Set<string>>(new Set())
  const toggle = (k: string) =>
    setShown((s) => {
      const next = new Set(s)
      if (!next.delete(k)) next.add(k)
      return next
    })
  return (
    <table className="env">
      <tbody>
        {env.map(({ key, value }) => {
          const secret = isSecret(key, value)
          const visible = !secret || shown.has(key)
          const label = t(visible ? 'env.hide' : 'env.reveal', { key })
          return (
            <tr key={key}>
              <th scope="row">{key}</th>
              <td>
                <span className="env-value">
                  <span className={visible ? undefined : 'env-masked'}>{visible ? value : '••••••••'}</span>
                  {secret && (
                    <button type="button" className="icon-btn icon-btn--sm" aria-label={label} title={label} onClick={() => toggle(key)}>
                      {visible ? <EyeOff size={14} /> : <Eye size={14} />}
                    </button>
                  )}
                </span>
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}
