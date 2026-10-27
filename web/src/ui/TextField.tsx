interface Props {
  id: string
  label: string
  hint?: string
  value: string
  onChange: (value: string) => void
  placeholder?: string
  disabled?: boolean
}

/** A labeled one-line monospace input with an optional hint under it. */
export function TextField({ id, label, hint, value, onChange, placeholder, disabled }: Props) {
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      <input id={id} className="input input--mono" value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} spellCheck={false} autoComplete="off" disabled={disabled} aria-describedby={hint ? `${id}-hint` : undefined} />
      {hint && <span className="hint" id={`${id}-hint`}>{hint}</span>}
    </div>
  )
}
