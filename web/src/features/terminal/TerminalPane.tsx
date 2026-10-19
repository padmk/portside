import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { SearchAddon } from '@xterm/addon-search'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { ArrowDownToLine, ChevronDown, ChevronUp, Eraser, ListFilter, Search } from 'lucide-react'
import '@xterm/xterm/css/xterm.css'
import { errorText, wsUrl } from '../../api/client'
import { useT } from '../../i18n'
import { usePrefs } from '../../state/prefs'
import { useUi } from '../../state/ui'
import { isModKey } from '../../ui/keys'
import { searchDecorations, terminalBackground, terminalTheme } from '../../theme/terminal'
import './terminal.css'

interface Props {
  /** WebSocket path of the output stream. */
  source: string
  /** Something is alive, so keystrokes have somewhere to go. */
  interactive: boolean
  /** `toggle` asks the user to switch typing on; `always` sends keystrokes right away, as a shell does. */
  input?: 'toggle' | 'always' | 'none'
  /** Reconnects when the stream closes. Without it the stream ends, as a shell session does. */
  reconnect?: boolean
  /** Resume a container log stream without clearing existing output. */
  resume?: boolean
  /** Shown over the screen once a stream that does not reconnect has ended. */
  ended?: ReactNode
  emptyText: string
  waitingText: string
  /**
   * Fetches the lines of the whole retained output that match a pattern, with
   * a little context, as plain text. Offers "Filter lines" next to the search.
   */
  filterLines?: (pattern: string) => Promise<string>
  /** Incremented to focus the search field. */
  searchSignal?: number
}

const FONT = '"Geist Mono Variable", ui-monospace, "SF Mono", Menlo, monospace'

const escapePattern = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

export function TerminalPane({ source, interactive, input = 'toggle', reconnect = true, resume = false, ended, emptyText, waitingText, filterLines, searchSignal = 0 }: Props) {
  const t = useT()
  const { mode, themeId } = usePrefs()
  const { focusTerminal } = useUi()
  const host = useRef<HTMLDivElement>(null)
  const searchInput = useRef<HTMLInputElement>(null)
  const term = useRef<Terminal | null>(null)
  const search = useRef<SearchAddon | null>(null)
  const typingRef = useRef(false)
  const [typing, setTyping] = useState(false)
  const [following, setFollowing] = useState(true)
  const [hasOutput, setHasOutput] = useState(false)
  const [streamError, setStreamError] = useState('')
  const [closed, setClosed] = useState(false)
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<{ index: number; count: number } | null>(null)
  const [filtering, setFiltering] = useState(false)
  const modeRef = useRef(mode)
  modeRef.current = mode
  const themeRef = useRef(themeId)
  themeRef.current = themeId

  const canType = input === 'always' ? interactive && !closed : input === 'toggle' && typing && interactive
  typingRef.current = canType

  useEffect(() => {
    const el = host.current
    if (!el) return
    let disposed = false
    let ws: WebSocket | null = null
    let timer: number | undefined
    let retry = 0
    let lastOutput = ''
    let lastContainer = ''
    let frame = 0
    const cleanups: (() => void)[] = []

    const start = () => {
      const tm = new Terminal({
        fontFamily: FONT,
        fontSize: 14,
        lineHeight: 1.3,
        minimumContrastRatio: 4.5,
        scrollback: 10000,
        cursorBlink: true,
        cursorInactiveStyle: 'none',
        theme: terminalTheme(themeRef.current),
        allowProposedApi: true,
        macOptionIsMeta: true,
      })
      const fit = new FitAddon()
      const sr = new SearchAddon()
      tm.loadAddon(fit)
      tm.loadAddon(sr)
      tm.loadAddon(new WebLinksAddon())
      tm.open(el)
      term.current = tm
      search.current = sr
      fit.fit()

      const sendResize = () => {
        if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ t: 'r', c: tm.cols, r: tm.rows }))
      }

      const connect = () => {
        const path = resume && lastOutput ? `${source}${source.includes('?') ? '&' : '?'}since=${encodeURIComponent(lastOutput)}&id=${encodeURIComponent(lastContainer)}` : source
        const sock = new WebSocket(wsUrl(path))
        let replay = !resume
        ws = sock
        sock.binaryType = 'arraybuffer'
        sock.onopen = () => {
          retry = 0
          sendResize()
        }
        sock.onmessage = (ev) => {
          if (typeof ev.data === 'string') {
            try {
              const message = JSON.parse(ev.data) as { t?: string; d?: string; id?: string }
              if (message.t === 'error') setStreamError(message.d ?? '')
              if (message.t === 'status') setStreamError('')
              if (message.t === 'cursor') {
                lastOutput = message.d ?? ''
                lastContainer = message.id ?? ''
              }
            } catch { /* Non-protocol text does not belong in the terminal. */ }
            return
          }
          if (!(ev.data instanceof ArrayBuffer)) return
          if (replay) { tm.reset(); replay = false }
          if (ev.data.byteLength > 0) setHasOutput(true)
          tm.write(new Uint8Array(ev.data))
        }
        sock.onclose = () => {
          if (disposed || ws !== sock) return
          if (!reconnect) {
            setClosed(true)
            return
          }
          timer = window.setTimeout(connect, Math.min(8000, 400 * 2 ** retry++))
        }
      }

      tm.onData((d) => {
        if (typingRef.current && ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ t: 'i', d }))
      })
      tm.onResize(sendResize)
      const syncFollow = () => {
        const b = tm.buffer.active
        setFollowing(b.viewportY >= b.baseY)
      }
      tm.onScroll(syncFollow)
      tm.onWriteParsed(syncFollow)
      sr.onDidChangeResults((r) => setResults(r.resultCount > 0 ? { index: r.resultIndex + 1, count: r.resultCount } : null))

      tm.attachCustomKeyEventHandler((e) => {
        if (e.type !== 'keydown') return true
        if (isModKey(e, 'f')) {
          e.preventDefault()
          searchInput.current?.focus()
          searchInput.current?.select()
          return false
        }
        // The command palette; the window's handler opens it.
        if (isModKey(e, 'k')) return false
        if (e.key === 'Escape' && !typingRef.current) {
          tm.blur()
          return false
        }
        return true
      })

      const ro = new ResizeObserver(() => {
        cancelAnimationFrame(frame)
        frame = requestAnimationFrame(() => {
          if (!disposed && el.clientWidth > 0 && el.clientHeight > 0) fit.fit()
        })
      })
      ro.observe(el)
      cleanups.push(() => ro.disconnect(), () => tm.dispose())
      connect()
      if (input === 'always') tm.focus()
    }

    // The font must be loaded before xterm measures a character cell.
    void document.fonts.load(`14px ${FONT}`).catch(() => undefined).then(() => {
      if (!disposed) start()
    })

    return () => {
      disposed = true
      window.clearTimeout(timer)
      cancelAnimationFrame(frame)
      ws?.close()
      cleanups.forEach((fn) => fn())
      term.current = null
      search.current = null
    }
  }, [source, reconnect, resume, input])

  useEffect(() => {
    if (term.current) term.current.options.theme = terminalTheme(themeId)
  }, [themeId])

  useEffect(() => {
    if (focusTerminal > 0) term.current?.focus()
  }, [focusTerminal])

  // Only a change asks for the search field; the value at mount belongs to an earlier page.
  const searchSeen = useRef(searchSignal)
  useEffect(() => {
    if (searchSignal === searchSeen.current) return
    searchSeen.current = searchSignal
    searchInput.current?.focus()
    searchInput.current?.select()
  }, [searchSignal])

  useEffect(() => {
    if (!interactive) setTyping(false)
  }, [interactive])

  const find = (dir: 'next' | 'prev', q = query) => {
    const s = search.current
    if (!s) return
    if (!q || filtering) {
      s.clearDecorations()
      setResults(null)
      return
    }
    const opts = { decorations: searchDecorations(modeRef.current), incremental: dir === 'next' && q !== query }
    if (dir === 'next') s.findNext(q, opts)
    else s.findPrevious(q, opts)
  }

  const toggleFilter = () => {
    const next = !filtering
    setFiltering(next)
    if (next) {
      search.current?.clearDecorations()
      setResults(null)
    } else if (query) {
      search.current?.findNext(query, { decorations: searchDecorations(modeRef.current) })
    }
    searchInput.current?.focus()
  }

  const filter = useLineFilter(filtering ? filterLines : undefined, query)

  return (
    <div className="term">
      <div className="term-bar">
        <div className="search-input term-search" data-filtering={filtering || undefined}>
          <Search size={14} aria-hidden="true" />
          <input
            ref={searchInput}
            className="input"
            type="search"
            value={query}
            placeholder={filtering ? t('terminal.filterPlaceholder') : t('terminal.search')}
            aria-label={filtering ? t('terminal.filterPlaceholder') : t('terminal.search')}
            onChange={(e) => {
              setQuery(e.target.value)
              find('next', e.target.value)
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                if (filtering) filter.refresh()
                else find(e.shiftKey ? 'prev' : 'next')
              } else if (e.key === 'Escape') {
                if (filtering && !query) toggleFilter()
                setQuery('')
                search.current?.clearDecorations()
                setResults(null)
                if (!filtering) term.current?.focus()
              }
            }}
          />
          {query && (
            <span className="term-count">
              {filtering ? (filter.lines ? t('terminal.lines', { n: filter.hits }) : '') : results ? `${results.index}/${results.count}` : t('terminal.noResults')}
            </span>
          )}
        </div>
        {filterLines && (
          <button type="button" className="icon-btn" aria-label={t('terminal.filter')} title={t('terminal.filterHint')} aria-pressed={filtering} onClick={toggleFilter}>
            <ListFilter size={16} />
          </button>
        )}
        <button type="button" className="icon-btn term-nav" aria-label={t('terminal.prev')} title={t('terminal.prev')} disabled={!query || filtering} onClick={() => find('prev')}>
          <ChevronUp size={16} />
        </button>
        <button type="button" className="icon-btn term-nav" aria-label={t('terminal.next')} title={t('terminal.next')} disabled={!query || filtering} onClick={() => find('next')}>
          <ChevronDown size={16} />
        </button>
        <span className="term-gap" />
        <button
          type="button"
          className="icon-btn"
          aria-label={t('terminal.follow')}
          title={t('terminal.follow')}
          aria-pressed={following}
          disabled={filtering}
          onClick={() => term.current?.scrollToBottom()}
        >
          <ArrowDownToLine size={16} />
        </button>
        <button type="button" className="icon-btn" aria-label={t('terminal.clear')} title={t('terminal.clearHint')} disabled={filtering} onClick={() => term.current?.clear()}>
          <Eraser size={16} />
        </button>
        {input === 'toggle' && (
          <button
            type="button"
            role="switch"
            className="switch"
            aria-checked={canType}
            disabled={!interactive}
            title={interactive ? t('terminal.typeHint') : t('terminal.typeOff')}
            onClick={() => {
              setTyping((v) => !v)
              if (!typing) term.current?.focus()
            }}
          >
            <span className="track" aria-hidden="true" />
            {t('terminal.type')}
          </button>
        )}
      </div>
      {streamError && <div className="banner banner--bad" role="alert">{streamError}</div>}
      <div className="term-screen" style={{ background: terminalBackground(themeId) }}>
        <div className="term-host" ref={host} data-typing={canType || undefined} />
        {!hasOutput && !closed && !filtering && <p className="term-empty">{interactive ? waitingText : emptyText}</p>}
        {closed && ended && <div className="term-ended">{ended}</div>}
        {filtering && <FilteredLines filter={filter} query={query} />}
      </div>
    </div>
  )
}

interface LineFilter {
  /** The lines of the last reply, or null before one arrives. */
  lines: string[] | null
  /** How many of them contain the query; the rest are context. */
  hits: number
  error: string
  /** The query the lines answer. */
  answered: string
  refresh: () => void
}

/** Asks for the lines that match the query, shortly after it stops changing. */
function useLineFilter(fetch: Props['filterLines'], query: string): LineFilter {
  const [reply, setReply] = useState<{ q: string; lines: string[] } | null>(null)
  const [error, setError] = useState('')
  const [tick, setTick] = useState(0)
  const seq = useRef(0)
  const now = useRef(false)
  const fetchRef = useRef(fetch)
  fetchRef.current = fetch
  const enabled = !!fetch

  useEffect(() => {
    const n = ++seq.current
    if (!enabled || !query) {
      setReply(null)
      setError('')
      return
    }
    const delay = now.current ? 0 : 250
    now.current = false
    const id = window.setTimeout(() => {
      fetchRef.current?.(escapePattern(query))
        .then((text) => {
          if (n !== seq.current) return
          const lines = text.replace(/\n$/, '')
          setReply({ q: query, lines: lines ? lines.split('\n') : [] })
          setError('')
        })
        .catch((e: unknown) => {
          if (n === seq.current) setError(errorText(e) ?? '')
        })
    }, delay)
    return () => window.clearTimeout(id)
  }, [enabled, query, tick])

  const q = reply?.q.toLowerCase() ?? ''
  return {
    lines: reply?.lines ?? null,
    hits: reply ? reply.lines.filter((l) => l.toLowerCase().includes(q)).length : 0,
    error,
    answered: reply?.q ?? '',
    refresh: () => {
      now.current = true
      setTick((n) => n + 1)
    },
  }
}

/** The filtered lines over the terminal, matches marked, context lines dimmed. */
function FilteredLines({ filter, query }: { filter: LineFilter; query: string }) {
  const t = useT()
  const { mode, themeId } = usePrefs()
  const list = useRef<HTMLDivElement>(null)
  const theme = terminalTheme(themeId)
  const mark = searchDecorations(mode)

  useEffect(() => {
    const el = list.current
    if (el) el.scrollTop = el.scrollHeight
  }, [filter.lines])

  let body: ReactNode
  if (filter.error) body = <p className="term-filter-note" role="alert">{filter.error}</p>
  else if (!query) body = <p className="term-filter-note">{t('terminal.filterPrompt')}</p>
  else if (filter.lines === null) body = null
  else if (filter.lines.length === 0) body = <p className="term-filter-note">{t('terminal.filterNone')}</p>
  else {
    const q = filter.answered.toLowerCase()
    body = filter.lines.map((line, i) => {
      if (line === '--') return <div key={i} className="term-filter-gap" aria-hidden="true" />
      const at = line.toLowerCase().indexOf(q)
      if (at < 0) return <div key={i} className="term-filter-line term-filter-line--context">{line || ' '}</div>
      const parts: ReactNode[] = []
      let from = 0
      for (let j = at; j >= 0; j = line.toLowerCase().indexOf(q, from)) {
        parts.push(line.slice(from, j), <mark key={j} style={{ background: mark.matchBackground, color: theme.foreground }}>{line.slice(j, j + q.length)}</mark>)
        from = j + q.length
      }
      parts.push(line.slice(from))
      return <div key={i} className="term-filter-line">{parts}</div>
    })
  }

  return (
    <div className="term-filter" ref={list} style={{ background: terminalBackground(themeId), color: theme.foreground }} role="region" aria-label={t('terminal.filter')} tabIndex={0}>
      {body}
    </div>
  )
}
