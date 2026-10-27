interface Props {
  checked: boolean
  onChange: (checked: boolean) => void
  label: string
  describedBy?: string
  disabled?: boolean
  id?: string
}

/** An on/off button (`role="switch"`) with its label inside. */
export function Switch({ checked, onChange, label, describedBy, disabled, id }: Props) {
  return (
    <button type="button" role="switch" id={id} className="switch" aria-checked={checked} aria-describedby={describedBy} disabled={disabled} onClick={() => onChange(!checked)}>
      <span className="track" aria-hidden="true" />
      {label}
    </button>
  )
}
