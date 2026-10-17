import { useCallback, useEffect, useRef, useState } from 'react'
import { KeyRound, Laptop, Loader2, Monitor, RefreshCw, Server, Smartphone, Unplug, X } from 'lucide-react'
import { api, ApiError, errorText } from '../../api/client'
import type { Mirror, PairedDevice, PairingCode, Peer, PeersResponse, RemoteConnection, RemoteShare } from '../../api/types'
import { brand } from '../../brand'
import { useT } from '../../i18n'
import { useRemote } from '../../state/live'
import { usePrefs } from '../../state/prefs'
import { closeDialog } from '../../state/ui'
import { toast, toastError } from '../../state/toasts'
import { Dialog } from '../../ui/Dialog'
import { useNow } from '../../ui/hooks'
import { useBusy } from '../servers/actions'
import { CopyButton } from '../servers/Address'
import { connectTo, disconnectRemote, pairWith } from './actions'
import './remote.css'

export function OsIcon({ os, size = 18 }: { os?: string; size?: number }) {
  const o = (os ?? '').toLowerCase()
  if (o === 'macos') return <Laptop size={size} aria-hidden="true" />
  if (o === 'ios' || o === 'android') return <Smartphone size={size} aria-hidden="true" />
  if (o === 'linux') return <Server size={size} aria-hidden="true" />
  return <Monitor size={size} aria-hidden="true" />
}

export function ComputersDialog() {
  const t = useT()
  const remote = useRemote()
  const [scan, setScan] = useState<PeersResponse | null>(null)
  const [scanning, setScanning] = useState(false)

  const load = useCallback(async () => {
    setScanning(true)
    try {
      setScan(await api.peers())
    } catch (e) {
      setScan({ tailnet: { available: false, error: e instanceof Error ? e.message : String(e) }, peers: [] })
    } finally {
      setScanning(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <Dialog
      title={t('remote.title')}
      size="md"
      className="computers"
      onClose={closeDialog}
      footer={
        <>
          <span className="foot-spacer" />
          <button type="button" className="btn" disabled={scanning} onClick={load}>
            <RefreshCw size={14} className={scanning ? 'spin' : undefined} aria-hidden="true" />
            {t('remote.scan')}
          </button>
          <button type="button" className="btn" onClick={closeDialog}>
            {t('common.close')}
          </button>
        </>
      }
    >
      {remote && <Connected remote={remote} />}
      <Peers scan={scan} remote={remote} />
      <ShareSection />
    </Dialog>
  )
}

function Connected({ remote }: { remote: RemoteConnection }) {
  const t = useT()
  const { locale } = usePrefs()
  const busy = useBusy('remote:disconnect')
  const name = remote.peer.name
  const since = new Date(remote.since)
  const meta = [remote.peer.os, remote.version && `${brand.name} ${remote.version}`, !Number.isNaN(since.getTime()) && t('remote.since', { time: since.toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit' }) })].filter(Boolean)
  return (
    <section className="here" aria-label={t('remote.connectedTo', { name })}>
      <div className="here-head">
        <span className="here-icon"><OsIcon os={remote.peer.os} size={20} /></span>
        <div className="here-id">
          <h3>{t('remote.connectedTo', { name })}</h3>
          <p className="mono">{remote.peer.dnsName}</p>
        </div>
        <button type="button" className="btn" disabled={busy} onClick={() => void disconnectRemote()}>
          {busy ? <Loader2 size={14} className="spin" aria-hidden="true" /> : <Unplug size={14} aria-hidden="true" />}
          {t('remote.disconnect')}
        </button>
      </div>
      <ul className="here-meta">
        {meta.map((m) => <li key={String(m)}>{m}</li>)}
      </ul>
      {remote.status !== 'open' && (
        <p className="here-status" role="status">
          <Loader2 size={14} className="spin" aria-hidden="true" />
          {t(remote.status === 'connecting' ? 'remote.connecting' : 'remote.reconnecting', { name })}
        </p>
      )}
      {remote.error && <p className="form-error" role="alert">{remote.error}</p>}
      {remote.mirrors.length > 0 && <Mirrors mirrors={remote.mirrors} />}
    </section>
  )
}

/** A suggestion for a busy port's local stand-in: the same digits, 10000 higher. */
const suggestPort = (port: number) => (port + 10000 <= 65535 ? String(port + 10000) : '')

/** Forwarded ports; a busy one can be forwarded to another local port instead. */
function Mirrors({ mirrors }: { mirrors: Mirror[] }) {
  const t = useT()
  const [editing, setEditing] = useState<number | null>(null)
  const [value, setValue] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const forward = async (port: number, local: number) => {
    setSaving(true)
    setError('')
    try {
      await api.forwardPort(port, local)
      setEditing(null)
    } catch (e) {
      if (editing === port) setError(errorText(e) ?? '')
      else toastError(e)
    } finally {
      setSaving(false)
    }
  }
  const local = Number(value)
  const valid = Number.isInteger(local) && local > 0 && local <= 65535

  return (
    <div className="mirrors">
      <h4>{t('remote.ports')}</h4>
      <ul>
        {mirrors.map((m) => {
          const address = `127.0.0.1:${m.local ?? m.port}`
          return (
            <li key={m.port} className={`mirror mirror--${m.state}`} title={m.server ?? m.container}>
              <span className="mono num">{m.local ? `${m.port} → ${m.local}` : m.port}</span>
              {t(m.state === 'mirrored' ? 'remote.mirrored' : 'remote.busy')}
              {m.local && m.state === 'mirrored' && (
                <CopyButton text={address} label={t('common.copyUrl', { url: address })} className="icon-btn mirror-btn" size={13} />
              )}
              {m.state === 'busy' && editing !== m.port && (
                <button
                  type="button"
                  className="mirror-action"
                  onClick={() => {
                    setEditing(m.port)
                    setValue(suggestPort(m.port))
                    setError('')
                  }}
                >
                  {t('remote.usePort')}
                </button>
              )}
              {m.local && (
                <button type="button" className="icon-btn mirror-btn" disabled={saving} aria-label={t('remote.samePort', { port: m.port })} title={t('remote.samePort', { port: m.port })} onClick={() => void forward(m.port, 0)}>
                  <X size={13} aria-hidden="true" />
                </button>
              )}
            </li>
          )
        })}
      </ul>
      {editing !== null && (
        <form
          className="port-form"
          onSubmit={(e) => {
            e.preventDefault()
            if (valid && !saving) void forward(editing, local)
          }}
        >
          <label htmlFor="mirror-local">{t('remote.forwardTo', { port: editing })}</label>
          <input
            id="mirror-local"
            className="input input--mono port-input"
            inputMode="numeric"
            value={value}
            autoFocus
            aria-invalid={!!error}
            onChange={(e) => {
              setValue(e.target.value.replace(/\D/g, '').slice(0, 5))
              setError('')
            }}
          />
          <button type="submit" className="btn btn--primary btn--sm" disabled={!valid || saving}>
            {saving && <Loader2 size={14} className="spin" aria-hidden="true" />}
            {t('remote.forward')}
          </button>
          <button type="button" className="btn btn--sm" onClick={() => setEditing(null)}>
            {t('common.cancel')}
          </button>
          {error && <p className="form-error" role="alert">{error}</p>}
        </form>
      )}
      <p className="mirrors-hint">{t('remote.portsHint')}</p>
    </div>
  )
}

/** Why a row asks for a pairing code: the user chose to pair, or connecting needs a new pairing. */
type PairReason = 'new' | 'required'

function Peers({ scan, remote }: { scan: PeersResponse | null; remote: RemoteConnection | null }) {
  const t = useT()
  const [connecting, setConnecting] = useState<string | null>(null)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [pairing, setPairing] = useState<Record<string, PairReason>>({})

  if (!scan) {
    return (
      <p className="scan-state peers-state" role="status">
        <Loader2 size={15} className="spin" aria-hidden="true" /> {t('remote.scanning')}
      </p>
    )
  }
  if (!scan.tailnet.available) {
    return (
      <div className="peers-empty" role="status">
        <p>{t('remote.noTailnet')}</p>
        {scan.tailnet.error && <p className="peers-detail mono">{scan.tailnet.error}</p>}
      </div>
    )
  }
  if (scan.peers.length === 0) {
    return (
      <div className="peers-empty">
        <p>{t('remote.none')}</p>
        <p className="peers-detail">{t('remote.noneHint')}</p>
        <code className="mono peers-cmd">{brand.name} share on</code>
      </div>
    )
  }

  const setError = (peer: string, message: string) => setErrors((x) => ({ ...x, [peer]: message }))
  const showPairing = (peer: string, reason: PairReason | null) =>
    setPairing((x) => {
      const next = { ...x }
      if (reason) next[peer] = reason
      else delete next[peer]
      return next
    })

  const connect = async (p: Peer) => {
    setConnecting(p.dnsName)
    setError(p.dnsName, '')
    try {
      await connectTo(p.dnsName)
    } catch (e) {
      if (e instanceof ApiError && e.code === 'pairing_required') showPairing(p.dnsName, 'required')
      else setError(p.dnsName, errorText(e) ?? '')
    } finally {
      setConnecting(null)
    }
  }

  return (
    <ul className="peers" aria-label={t('remote.title')}>
      {scan.peers.map((p) => {
        // The live connection decides, so a disconnect shows at once without a new scan.
        const current = p.dnsName === remote?.peer.dnsName
        const error = errors[p.dnsName]
        const reason = current ? undefined : pairing[p.dnsName]
        return (
          <li key={p.dnsName} className={`peer${current ? ' peer--current' : ''}`}>
            <span className="peer-icon"><OsIcon os={p.os} /></span>
            <div className="peer-id">
              <span className="peer-name" title={p.dnsName}>{p.name}</span>
              <span className="peer-meta">
                <span>{p.os}</span>
                {p.version && <span>{brand.name} {p.version}</span>}
              </span>
            </div>
            <span className="peer-servers">
              {!p.paired || !p.servers
                ? t('remote.notPaired')
                : p.servers.total > 0
                  ? t('remote.servers', { running: p.servers.running, n: p.servers.total })
                  : t('remote.noServers')}
            </span>
            {current ? (
              <span className="chip peer-chip">{t('remote.connected')}</span>
            ) : reason ? (
              <button type="button" className="btn btn--sm" onClick={() => showPairing(p.dnsName, null)}>
                {t('common.cancel')}
              </button>
            ) : p.paired ? (
              <button
                type="button"
                className="btn btn--primary btn--sm"
                disabled={connecting !== null}
                aria-label={t('remote.connectTo', { name: p.name })}
                onClick={() => void connect(p)}
              >
                {connecting === p.dnsName && <Loader2 size={14} className="spin" aria-hidden="true" />}
                {t('remote.connect')}
              </button>
            ) : (
              <button
                type="button"
                className="btn btn--sm"
                disabled={connecting !== null}
                aria-label={t('remote.pairWith', { name: p.name })}
                onClick={() => {
                  setError(p.dnsName, '')
                  showPairing(p.dnsName, 'new')
                }}
              >
                <KeyRound size={14} aria-hidden="true" />
                {t('remote.pair')}
              </button>
            )}
            {reason && <PairForm peer={p} reason={reason} />}
            {error && !reason && <p className="form-error peer-error" role="alert">{error}</p>}
          </li>
        )
      })}
    </ul>
  )
}

const CODE_LENGTH = 8

/** Characters a code can contain: no 0, 1, I or O, which read alike. */
const CODE_CHARS = /[^ABCDEFGHJKLMNPQRSTUVWXYZ23456789]/g

/** Code characters only, upper case, at most one code long, shown as XXXX-XXXX. */
function formatCode(input: string): string {
  const raw = input.toUpperCase().replace(CODE_CHARS, '').slice(0, CODE_LENGTH)
  return raw.length > 4 ? `${raw.slice(0, 4)}-${raw.slice(4)}` : raw
}

/** Takes the pairing code another computer issued, pairs with it and switches to it. */
function PairForm({ peer, reason }: { peer: Peer; reason: PairReason }) {
  const t = useT()
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const complete = code.replace('-', '').length === CODE_LENGTH
  const id = `pair-${peer.dnsName}`

  const submit = async () => {
    if (!complete || busy) return
    setBusy(true)
    setError('')
    try {
      await pairWith(peer.dnsName, code)
    } catch (e) {
      setError(e instanceof ApiError && e.code === 'pairing_failed' ? t('remote.pairingFailed') : (errorText(e) ?? ''))
      setBusy(false)
    }
  }

  return (
    <form
      className="pair-form"
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
    >
      {reason === 'required' && <p className="pair-note" role="status">{t('remote.pairingRequired', { name: peer.name })}</p>}
      <div className="pair-row">
        <input
          className="input input--mono pair-input"
          value={code}
          placeholder="XXXX-XXXX"
          aria-label={t('remote.codeFrom', { name: peer.name })}
          aria-describedby={`${id}-hint`}
          aria-invalid={!!error}
          autoFocus
          autoComplete="one-time-code"
          autoCapitalize="characters"
          autoCorrect="off"
          spellCheck={false}
          maxLength={CODE_LENGTH + 1}
          onChange={(e) => {
            setCode(formatCode(e.target.value))
            setError('')
          }}
          onPaste={(e) => {
            e.preventDefault()
            setCode(formatCode(e.clipboardData.getData('text')))
            setError('')
          }}
        />
        <button type="submit" className="btn btn--primary btn--sm" disabled={!complete || busy}>
          {busy && <Loader2 size={14} className="spin" aria-hidden="true" />}
          {t('remote.pairConnect')}
        </button>
      </div>
      {error && <p className="form-error" role="alert">{error}</p>}
      <p className="pair-hint" id={`${id}-hint`}>
        {t('remote.pairHint', { name: peer.name })} <code className="mono">{brand.name} share code</code>
      </p>
    </form>
  )
}

/** Remaining time as m:ss. */
const countdown = (ms: number) => {
  const s = Math.max(0, Math.ceil(ms / 1000))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

function ShareSection() {
  const t = useT()
  const [share, setShare] = useState<RemoteShare | null>(null)
  const [saving, setSaving] = useState(false)
  const [code, setCode] = useState<PairingCode | null>(null)
  const [issuing, setIssuing] = useState(false)
  const [codeError, setCodeError] = useState('')

  useEffect(() => {
    let live = true
    api.remote().then((r) => live && setShare(r.share), toastError)
    return () => {
      live = false
    }
  }, [])

  const issue = useCallback(async () => {
    setIssuing(true)
    setCodeError('')
    try {
      setCode(await api.pairingCode())
    } catch (e) {
      setCode(null)
      setCodeError(errorText(e) ?? '')
    } finally {
      setIssuing(false)
    }
  }, [])

  const toggle = async () => {
    if (!share) return
    setSaving(true)
    try {
      const next = (await api.share(!share.enabled)).share
      setShare(next)
      setCode(null)
      setCodeError('')
      // Turning sharing on is the moment to pair a device, so a code is ready at once.
      if (next.enabled) void issue()
    } catch (e) {
      toastError(e)
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className="share" aria-labelledby="share-title">
      <h3 id="share-title">{t('remote.thisComputer')}</h3>
      <button type="button" role="switch" className="switch" aria-checked={!!share?.enabled} disabled={!share || saving} onClick={() => void toggle()}>
        <span className="track" aria-hidden="true" />
        {t('remote.share')}
        {saving && <Loader2 size={14} className="spin" aria-hidden="true" />}
      </button>
      <p className="share-note">{t('remote.shareWarning')}</p>
      {share?.enabled && share.active && share.url && <p className="share-url mono">{t('remote.reachable', { url: share.url })}</p>}
      {share?.enabled && !share.active && !share.error && <p className="share-note">{t('remote.notReachable')}</p>}
      {share?.error && <p className="form-error" role="alert">{share.error}</p>}
      {share?.enabled &&
        (code ? (
          <CodeCard
            code={code}
            known={share.devices}
            issuing={issuing}
            onNew={() => void issue()}
            onPaired={(next, device) => {
              setShare(next)
              setCode(null)
              toast(t('remote.pairedWith', { name: device.name }))
            }}
          />
        ) : (
          <button type="button" className="btn btn--sm" disabled={issuing} onClick={() => void issue()}>
            {issuing ? <Loader2 size={14} className="spin" aria-hidden="true" /> : <KeyRound size={14} aria-hidden="true" />}
            {t('remote.pairDevice')}
          </button>
        ))}
      {codeError && <p className="form-error" role="alert">{codeError}</p>}
      {share && share.devices.length > 0 && <Devices devices={share.devices} onChange={setShare} />}
    </section>
  )
}

/** How often the code card checks whether a device has paired with the code shown. */
const PAIR_POLL_MS = 3000

/** The code to enter on the other computer, with its expiry and a way to get a new one. */
interface CodeCardProps {
  code: PairingCode
  /** Devices paired before this code was shown. */
  known: PairedDevice[]
  issuing: boolean
  onNew: () => void
  onPaired: (share: RemoteShare, device: PairedDevice) => void
}

function CodeCard({ code, known, issuing, onNew, onPaired }: CodeCardProps) {
  const t = useT()
  const now = useNow(1000)
  const left = new Date(code.expiresAt).getTime() - now
  const expired = !(left > 0)
  const onPairedRef = useRef(onPaired)
  onPairedRef.current = onPaired
  const knownRef = useRef(known)
  knownRef.current = known

  // A device that pairs uses up the code; notice it and show the device in the list.
  useEffect(() => {
    if (expired) return
    const before = new Set(knownRef.current.map((d) => d.id))
    let live = true
    const check = async () => {
      try {
        const share = (await api.remote()).share
        if (!live) return
        const added = share.devices.find((d) => !before.has(d.id))
        if (added) onPairedRef.current(share, added)
      } catch {
        /* the next check retries */
      }
    }
    const id = window.setInterval(() => void check(), PAIR_POLL_MS)
    return () => {
      live = false
      window.clearInterval(id)
    }
  }, [code.code, expired])

  return (
    <div className="pair-code">
      <p className={`pair-code-value mono${expired ? ' pair-code-value--expired' : ''}`}>
        {formatCode(code.code)}
      </p>
      <div className="pair-code-text">
        <p>{t('remote.codeHelp')}</p>
        <p className="pair-code-expiry num" role={expired ? 'status' : undefined}>
          {expired ? t('remote.codeExpired') : t('remote.codeExpires', { time: countdown(left) })}
        </p>
      </div>
      <button type="button" className="btn btn--sm" disabled={issuing} onClick={onNew}>
        {issuing ? <Loader2 size={14} className="spin" aria-hidden="true" /> : <RefreshCw size={14} aria-hidden="true" />}
        {t('remote.newCode')}
      </button>
    </div>
  )
}

/** Devices paired with this computer, each with a way to revoke it. */
function Devices({ devices, onChange }: { devices: PairedDevice[]; onChange: (share: RemoteShare) => void }) {
  const t = useT()
  const { locale } = usePrefs()
  const [confirm, setConfirm] = useState<string | null>(null)
  const [removing, setRemoving] = useState<string | null>(null)

  const remove = async (d: PairedDevice) => {
    setRemoving(d.id)
    try {
      onChange((await api.revokeDevice(d.id)).share)
      setConfirm(null)
    } catch (e) {
      toastError(e)
    } finally {
      setRemoving(null)
    }
  }

  return (
    <div className="devices">
      <h4>{t('remote.devices')}</h4>
      <ul>
        {devices.map((d) => {
          const paired = new Date(d.pairedAt)
          const date = Number.isNaN(paired.getTime()) ? '' : paired.toLocaleDateString(locale, { day: 'numeric', month: 'short', year: 'numeric' })
          return (
            <li key={d.id} className="device">
              <span className="device-icon"><OsIcon os={d.os} size={16} /></span>
              <span className="device-id">
                <span className="device-name" title={d.dnsName}>{d.name}</span>
                <span className="device-meta">
                  {d.os && <span>{d.os}</span>}
                  {date && <span>{t('remote.pairedOn', { date })}</span>}
                </span>
              </span>
              {confirm === d.id ? (
                <span className="device-confirm" role="group" aria-label={t('remote.removeName', { name: d.name })}>
                  <span className="device-confirm-text">{t('remote.removeConfirm')}</span>
                  <button type="button" className="btn btn--danger btn--sm" disabled={removing === d.id} onClick={() => void remove(d)} autoFocus>
                    {removing === d.id && <Loader2 size={14} className="spin" aria-hidden="true" />}
                    {t('remote.remove')}
                  </button>
                  <button type="button" className="btn btn--sm" onClick={() => setConfirm(null)}>
                    {t('common.cancel')}
                  </button>
                </span>
              ) : (
                <button type="button" className="btn btn--ghost btn--sm" aria-label={t('remote.removeName', { name: d.name })} onClick={() => setConfirm(d.id)}>
                  {t('remote.remove')}
                </button>
              )}
            </li>
          )
        })}
      </ul>
    </div>
  )
}
