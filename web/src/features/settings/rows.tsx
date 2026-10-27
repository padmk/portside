import type { ReactNode } from 'react'
import { Loader2 } from 'lucide-react'

/** A titled card of setting rows; `note` explains what the whole group applies to. */
export function SettingGroup({ id, title, note, children }: { id: string; title?: string; note?: ReactNode; children: ReactNode }) {
  return (
    <section className="set-group" aria-labelledby={title ? `${id}-title` : undefined}>
      {title && <h3 id={`${id}-title`}>{title}</h3>}
      {note && <p className="set-group-note">{note}</p>}
      <div className="set-card">{children}</div>
    </section>
  )
}

interface RowProps {
  id: string
  label: ReactNode
  hint?: ReactNode
  /** The control, beside the label; `stack` puts it under the label at full width. */
  children: ReactNode
  stack?: boolean
  /** Whether the label names a form field (`<label for>`); otherwise the control is labelled by it. */
  field?: boolean
}

/** One setting: its label and hint on the left, the control on the right. */
export function SettingRow({ id, label, hint, children, stack, field }: RowProps) {
  return (
    <div className={`set-row${stack ? ' set-row--stack' : ''}`}>
      <div className="set-row-text">
        {field ? (
          <label className="set-row-label" htmlFor={id}>
            {label}
          </label>
        ) : (
          <span className="set-row-label" id={`${id}-label`}>
            {label}
          </span>
        )}
        {hint && (
          <p className="set-row-hint" id={`${id}-hint`}>
            {hint}
          </p>
        )}
      </div>
      <div className="set-row-control">{children}</div>
    </div>
  )
}

/** A switch row: the label and hint describe it. */
export function SwitchRow({ id, label, hint, checked, onChange, busy }: { id: string; label: ReactNode; hint?: ReactNode; checked: boolean; onChange: (on: boolean) => void; busy?: boolean }) {
  return (
    <SettingRow id={id} label={label} hint={hint}>
      {busy && <Loader2 size={14} className="spin" aria-hidden="true" />}
      <button
        type="button"
        role="switch"
        id={id}
        className="set-switch"
        aria-checked={checked}
        aria-labelledby={`${id}-label`}
        aria-describedby={hint ? `${id}-hint` : undefined}
        disabled={busy}
        onClick={() => onChange(!checked)}
      >
        <span className="track" aria-hidden="true" />
      </button>
    </SettingRow>
  )
}

/** A one-line monospace input row. */
export function TextRow({ id, label, hint, value, onChange, placeholder, inputMode }: { id: string; label: ReactNode; hint?: ReactNode; value: string; onChange: (v: string) => void; placeholder?: string; inputMode?: 'numeric' }) {
  return (
    <SettingRow id={id} label={label} hint={hint} field>
      <input
        id={id}
        className="input input--mono"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        inputMode={inputMode}
        spellCheck={false}
        autoComplete="off"
        aria-describedby={hint ? `${id}-hint` : undefined}
      />
    </SettingRow>
  )
}
