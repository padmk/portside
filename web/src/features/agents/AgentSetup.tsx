import { useEffect, useRef, useState } from 'react'
import { AlertTriangle, CheckCircle2, XCircle } from 'lucide-react'
import { api, errorText } from '../../api/client'
import type { AgentItem, AgentResult, AgentsView } from '../../api/types'
import { useT, type Translate } from '../../i18n'
import { CopyButton } from '../../ui/CopyButton'
import './agents.css'

const AGENTS: Record<string, string> = { claude: 'Claude Code', codex: 'Codex', gemini: 'Gemini CLI' }

function label(t: Translate, item: AgentItem): string {
  const [agent = '', kind] = item.id.split('-')
  const name = AGENTS[agent] ?? agent
  return t(kind === 'skill' ? 'agents.skill' : 'agents.mcp', { agent: name })
}

const fixable = (i: AgentItem) => i.state === 'missing' || i.state === 'outdated'

/**
 * The coding agents of this computer: what each installed agent has (MCP server, skill), a way to
 * connect the missing parts, and the prompt that lets an agent do the whole setup itself.
 */
export function AgentSetup({ reveal = false }: { reveal?: boolean }) {
  const t = useT()
  const root = useRef<HTMLDivElement>(null)
  const [view, setView] = useState<AgentsView | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [results, setResults] = useState<Record<string, AgentResult>>({})
  const [busy, setBusy] = useState(false)

  const load = () =>
    api.agents().then(
      (v) => {
        setView(v)
        setSelected(new Set(v.items.filter(fixable).map((i) => i.id)))
        setError(null)
      },
      (e) => setError(errorText(e)),
    )
  useEffect(() => {
    void load()
  }, [])

  // Scrolled into view once loaded, when asked to (the section it sits in, with its heading).
  const loaded = view !== null || error !== null
  useEffect(() => {
    if (reveal && loaded) root.current?.closest('section')?.scrollIntoView({ block: 'start' })
  }, [reveal, loaded])

  const connect = async () => {
    setBusy(true)
    try {
      const list = await api.applyAgents([...selected])
      setResults(Object.fromEntries(list.map((r) => [r.id, r])))
      await load()
    } catch (e) {
      setError(errorText(e))
    } finally {
      setBusy(false)
    }
  }

  if (error) return <p ref={root} className="agents-error">{error}</p>
  if (!view) return <p className="agents-note">{t('common.loading')}</p>
  if (view.items.length === 0) return <p ref={root} className="agents-note">{t('agents.none')}</p>

  const todo = view.items.filter((i) => fixable(i) && selected.has(i.id))
  return (
    <div ref={root} className="agents">
      <ul className="agents-list">
        {view.items.map((item) => (
          <li key={item.id}>
            <Row item={item} label={label(t, item)} result={results[item.id]} selected={selected.has(item.id)} disabled={busy} onToggle={(on) => {
              const next = new Set(selected)
              if (on) next.add(item.id)
              else next.delete(item.id)
              setSelected(next)
            }} />
          </li>
        ))}
      </ul>
      <div className="agents-actions">
        {view.items.some(fixable) && (
          <button type="button" className="btn btn--primary" disabled={busy || todo.length === 0} onClick={() => void connect()}>
            {busy ? t('agents.connecting') : t('agents.connect')}
          </button>
        )}
        <CopyButton text={view.prompt} label={t('agents.prompt')} className="btn" size={14}>
          {t('agents.prompt')}
        </CopyButton>
      </div>
      <p className="agents-note">{t('agents.promptHint')}</p>
    </div>
  )
}

function Row({ item, label, result, selected, disabled, onToggle }: {
  item: AgentItem
  label: string
  result?: AgentResult
  selected: boolean
  disabled: boolean
  onToggle: (on: boolean) => void
}) {
  const t = useT()
  if (item.state === 'ok') {
    return (
      <span className="agents-row">
        <CheckCircle2 size={16} className="agents-ok" aria-hidden="true" />
        {label}
        <span className="agents-state">{t('agents.connected')}</span>
      </span>
    )
  }
  if (fixable(item) && !result) {
    return (
      <label className="check agents-row">
        <input type="checkbox" checked={selected} disabled={disabled} onChange={(e) => onToggle(e.target.checked)} />
        {item.state === 'outdated' ? t('agents.update', { label }) : label}
      </label>
    )
  }
  // A failed attempt, a skill someone else wrote, or an agent CLI that was not found: the command to run by hand.
  const command = result?.command ?? item.command
  const note = result?.error
    ? t('agents.failed', { error: result.error })
    : item.state === 'foreign'
      ? t('agents.foreign')
      : t('agents.unavailable', { command: item.command.split(' ')[0] ?? '' })
  return (
    <div className="agents-manual">
      <span className="agents-row">
        {result ? <XCircle size={16} className="agents-bad" aria-hidden="true" /> : <AlertTriangle size={16} className="agents-warn" aria-hidden="true" />}
        {label}
      </span>
      <p className="agents-note">{note}</p>
      <div className="cli-line">
        <code>{command}</code>
        <CopyButton text={command} label={t('common.copyCommand', { command })} size={13} />
      </div>
    </div>
  )
}
