import { Plus, X } from 'lucide-react'
import { useT } from '../i18n'
import './fields.css'

export interface EnvRow {
  k: string
  v: string
}

export const envRows = (env: Record<string, string> | null | undefined): EnvRow[] => Object.entries(env ?? {}).map(([k, v]) => ({ k, v }))

/** The variables entered, without rows that have no name; undefined when there are none. */
export function envMap(rows: EnvRow[]): Record<string, string> | undefined {
  const env: Record<string, string> = {}
  for (const { k, v } of rows) if (k.trim()) env[k.trim()] = v
  return Object.keys(env).length ? env : undefined
}

/** Rows of environment variables, each a name and a value, with add and remove buttons. */
export function EnvEditor({ legend, rows, onChange }: { legend: string; rows: EnvRow[]; onChange: (rows: EnvRow[]) => void }) {
  const t = useT()
  const edit = (i: number, row: Partial<EnvRow>) => onChange(rows.map((r, j) => (j === i ? { ...r, ...row } : r)))
  return (
    <fieldset className="field env-field">
      <legend className="label">{legend}</legend>
      {rows.map((row, i) => (
        <div className="env-row" key={i}>
          <input className="input input--mono" value={row.k} aria-label={t('form.envKey')} placeholder="KEY" spellCheck={false} onChange={(e) => edit(i, { k: e.target.value })} />
          <input className="input input--mono" value={row.v} aria-label={t('form.envValue')} placeholder="value" spellCheck={false} onChange={(e) => edit(i, { v: e.target.value })} />
          <button type="button" className="icon-btn" aria-label={t('form.envRemove')} title={t('form.envRemove')} onClick={() => onChange(rows.filter((_, j) => j !== i))}>
            <X size={15} />
          </button>
        </div>
      ))}
      <button type="button" className="btn btn--ghost env-add" onClick={() => onChange([...rows, { k: '', v: '' }])}>
        <Plus size={14} aria-hidden="true" />
        {t('form.envAdd')}
      </button>
    </fieldset>
  )
}
