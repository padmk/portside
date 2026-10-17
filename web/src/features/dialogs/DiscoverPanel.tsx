import { useCallback, useEffect, useMemo, useState } from 'react'
import { Loader2, Search } from 'lucide-react'
import { api, errorText, isStale } from '../../api/client'
import type { State, Suggestion } from '../../api/types'
import { useT } from '../../i18n'
import { applyState } from '../../state/live'
import { closeDialog } from '../../state/ui'
import { DialogFoot, DialogMain } from '../../ui/Dialog'
import { NAME_RE } from '../servers/format'

interface Pick {
  s: Suggestion
  checked: boolean
  name: string
  port: string
  error?: string
}

interface Props {
  state: State
  onManual: () => void
}

export function DiscoverPanel({ state, onManual }: Props) {
  const t = useT()
  const [root, setRoot] = useState(state.config.root)
  const [picks, setPicks] = useState<Pick[] | null>(null)
  const [scanned, setScanned] = useState('')
  const [error, setError] = useState('')
  const [group, setGroup] = useState('')
  const [adding, setAdding] = useState(false)

  const scan = useCallback(
    async (dir?: string) => {
      setPicks(null)
      setError('')
      try {
        const r = await api.discover(dir)
        setScanned(r.root)
        setPicks(r.suggestions.map((s) => ({ s, checked: true, name: s.name, port: s.port ? String(s.port) : '' })))
      } catch (e) {
        if (isStale(e)) return
        setError(errorText(e) ?? '')
        setPicks([])
      }
    },
    [],
  )

  useEffect(() => {
    void scan()
  }, [scan])

  const existing = useMemo(() => new Set(state.servers.map((s) => s.name)), [state.servers])
  const selected = (picks ?? []).filter((p) => p.checked)

  const nameError = (p: Pick): string => {
    if (!NAME_RE.test(p.name)) return t('form.nameInvalid')
    if (existing.has(p.name)) return t('form.nameTaken')
    if (selected.some((o) => o !== p && o.name === p.name)) return t('form.nameTaken')
    return ''
  }
  const portError = (p: Pick): string => {
    if (p.s.noPort || p.port === '') return ''
    const n = Number(p.port)
    return Number.isInteger(n) && n >= 1 && n <= 65535 ? '' : t('form.portInvalid')
  }
  const invalid = selected.some((p) => nameError(p) || portError(p))

  const update = (s: Suggestion, patch: Partial<Pick>) =>
    setPicks((list) => list?.map((p) => (p.s === s ? { ...p, ...patch } : p)) ?? null)

  const addSelected = useCallback(async () => {
    if (!picks) return
    setAdding(true)
    const remaining: Pick[] = []
    for (const p of picks) {
      if (!p.checked) {
        remaining.push(p)
        continue
      }
      try {
        applyState(
          await api.createServer({
            name: p.name,
            dir: p.s.dirConfig,
            cmd: p.s.cmd,
            port: p.s.noPort ? 0 : p.port === '' ? undefined : Number(p.port),
            group: group.trim() || undefined,
            stopTimeout: p.s.stopTimeout || undefined,
          }),
        )
      } catch (e) {
        // The dashboard shows another computer now; this dialog's work is moot.
        if (isStale(e)) return
        remaining.push({ ...p, error: errorText(e) ?? '' })
      }
    }
    setAdding(false)
    if (remaining.every((p) => !p.error)) {
      closeDialog()
      return
    }
    setPicks(remaining)
  }, [picks, group])

  return (
    <>
    <DialogMain>
    <div className="discover">
      <form
        className="scan-bar"
        onSubmit={(e) => {
          e.preventDefault()
          void scan(root)
        }}
      >
        <div className="field grow">
          <label htmlFor="scan-root">{t('discover.folder')}</label>
          <input id="scan-root" className="input input--mono" value={root} onChange={(e) => setRoot(e.target.value)} spellCheck={false} />
        </div>
        <div className="field">
          <label htmlFor="scan-group">{t('form.group')}</label>
          <input id="scan-group" className="input" list="group-options" value={group} onChange={(e) => setGroup(e.target.value)} placeholder={t('form.groupNone')} />
          <datalist id="group-options">
            {state.groups.map((g) => (
              <option key={g} value={g} />
            ))}
          </datalist>
        </div>
        <button type="submit" className="btn" disabled={picks === null}>
          <Search size={14} aria-hidden="true" />
          {t('discover.scan')}
        </button>
      </form>

      {picks === null && (
        <p className="scan-state" role="status">
          <Loader2 size={15} className="spin" aria-hidden="true" /> {t('discover.scanning')}
        </p>
      )}
      {error && <p className="form-error" role="alert">{error}</p>}
      {picks && picks.length === 0 && !error && <p className="scan-state">{t('discover.none', { root: scanned })}</p>}

      {picks && picks.length > 0 && (
        <>
          <div className="pick-head">
            <label className="check">
              <input
                type="checkbox"
                checked={picks.every((p) => p.checked)}
                onChange={(e) => setPicks(picks.map((p) => ({ ...p, checked: e.target.checked })))}
              />
              {t('discover.found', { n: picks.length })}
            </label>
          </div>
          <ul className="picks">
            {picks.map((p) => {
              const ne = p.checked ? nameError(p) : ''
              const pe = p.checked ? portError(p) : ''
              return (
                <li key={`${p.s.dir}:${p.s.cmd}`} className="pick" data-checked={p.checked || undefined}>
                  <input type="checkbox" className="pick-check" checked={p.checked} aria-label={t('discover.select', { name: p.s.name })} onChange={(e) => update(p.s, { checked: e.target.checked })} />
                  <div className="pick-fields">
                    <input className="input" value={p.name} aria-label={t('form.name')} aria-invalid={!!ne} onChange={(e) => update(p.s, { name: e.target.value.trim(), error: undefined })} spellCheck={false} />
                    <span className="chip">{p.s.kind}</span>
                    <input
                      className="input input--mono pick-port"
                      inputMode="numeric"
                      value={p.s.noPort ? '' : p.port}
                      placeholder={p.s.noPort ? t('detail.noPort') : t('form.portAuto')}
                      disabled={p.s.noPort}
                      aria-label={t('form.port')}
                      aria-invalid={!!pe}
                      onChange={(e) => update(p.s, { port: e.target.value.trim() })}
                    />
                  </div>
                  <p className="pick-path">
                    <span className="mono">{p.s.dirDisplay}</span>
                    {p.s.directoryConfigured && <span className="hint"> · {t('discover.directoryConfigured')}</span>}
                  </p>
                  <p className="pick-cmd mono">{p.s.cmd}</p>
                  {(ne || pe || p.error) && <p className="form-error">{ne || pe || p.error}</p>}
                </li>
              )
            })}
          </ul>
        </>
      )}
    </div>
    </DialogMain>
    <DialogFoot>
      <button type="button" className="btn" onClick={onManual}>
        {t('dialog.addManually')}
      </button>
      <span className="foot-spacer" />
      <button type="button" className="btn" onClick={closeDialog}>
        {t('common.cancel')}
      </button>
      <button type="button" className="btn btn--primary" disabled={adding || selected.length === 0 || invalid} onClick={addSelected}>
        {adding && <Loader2 size={14} className="spin" aria-hidden="true" />}
        {t('discover.addSelected', { n: selected.length })}
      </button>
    </DialogFoot>
    </>
  )
}
