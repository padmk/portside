export type Status = 'stopped' | 'starting' | 'running' | 'stopping' | 'crashed' | 'exited' | 'blocked'

export type RestartPolicy = 'no' | 'on-failure' | 'always'

export interface Listener {
  port: number
  pid: number
  command: string
  cwd?: string
  /** Local addresses the process listens on at this port, such as `127.0.0.1`, `[::1]` or `*`. */
  addresses?: string[]
}

/** Where a listener can be reached from: this computer, Tailscale addresses only, or the network. */
export type Exposure = 'loopback' | 'tailnet' | 'network'

export interface ServerView {
  name: string
  group?: string
  dir: string
  dirDisplay: string
  cmd: string
  port: number
  portAuto?: boolean
  url?: string
  alias?: string
  status: Status
  /** Running but no longer answering on its port or health path (the note says why); not restarted. */
  unready?: boolean
  /** A task (`oneshot = true`): it runs to completion and is `starting` while it runs, never `running`. */
  oneshot?: boolean
  /** A task whose last run exited with code 0 (status `exited`). */
  done?: boolean
  /** Crontab expression (local time) that starts the server. */
  schedule?: string
  /** When the schedule next starts it; absent while the server is disabled. */
  nextRun?: string
  /** When the latest scheduled run began. */
  lastRun?: string
  /** File patterns, relative to `dir`, whose changes restart the server. */
  watch?: string[]
  note?: string
  pid?: number
  startedAt?: string
  readyAt?: string
  exitedAt?: string
  exitCode?: number
  listening: boolean
  external?: Listener
  /** The connected computer whose server answers on this stopped server's port, forwarded here. */
  forwardedFrom?: string
  stale?: boolean
  removed?: boolean
  enabled: boolean
  dependsOn?: string[]
  projects?: string[]
  restart: RestartPolicy | string
  restarts?: number
  cpu: number
  mem: number
  cpuHistory?: number[]
  memHistory?: number[]
  hasIcon: boolean
  /** The last few plain output lines, oldest first. */
  tail?: string[]
  wanted: boolean
  /** The tool environment the live process runs under, such as `direnv` or `mise` (`custom` for a configured `wrap`). */
  tool?: string
  /** A crashed or exited server waits for its automatic restart. */
  restartPending?: boolean
  /** The pending restart is a retry after `retry_after`, scheduled once the automatic restarts gave up. */
  retrying?: boolean
  /** Ports the process tree listens on, when they differ from the configured port. */
  listenPorts?: number[]
  /** A copy of the configured server `origin` running in the checkout `worktree`; not in the config file. */
  temporary?: boolean
  origin?: string
  worktree?: string
  /** Where the running server's listeners can be reached from; absent until read. */
  exposure?: Exposure
  /** May be reachable from the network (it listens on more than loopback and Tailscale) although the config does not set `lan = true`. */
  networkWarning?: boolean
  /** The server's `lan` setting: meant to be reachable from other devices. */
  lan?: boolean
  /** The tool its command starts, such as vite or django, while networkWarning is set; picks the fix. */
  kind?: string
}

export interface Health {
  app: string
  name: string
  version: string
  pid: number
  port: number
  startedAt: string
  /** Spacing of the cpuHistory and memHistory samples. */
  sampleIntervalMs?: number
  /** Spacing of usage samples while no client shows usage; set by daemons that take {"usage": bool} on the events stream. */
  idleSampleIntervalMs?: number
}

export interface ConfigInfo {
  path: string
  pathDisplay: string
  root: string
  error?: string
  warnings?: string[]
  stateDir: string
  /** Changes whenever the config file's text changes. */
  revision: string
  notify: boolean
}

export interface State {
  warnings?: string[]
  daemon: Health
  config: ConfigInfo
  servers: ServerView[]
  groups: string[]
  docker: DockerState
  /** Set while this computer shows another computer's dashboard. */
  remote?: RemoteConnection | null
  /** Devices paired with this computer; absent when there are none and for those devices themselves. */
  paired?: PairedDevice[]
}

export type ContainerState = 'created' | 'running' | 'paused' | 'restarting' | 'removing' | 'exited' | 'dead'

export interface ContainerPort {
  private: number
  public: number
  proto: string
  ip?: string
  /** Where the host-side listener can be reached from, for a local engine. */
  exposure?: Exposure
  /** May be reachable from the network although the owning server does not set `lan = true`. */
  networkWarning?: boolean
}

/** The last compose recreate or rebuild run for a container. */
export interface ContainerJob {
  kind: 'recreate' | 'rebuild' | 'pull' | 'start' | 'stop' | 'down'
  running: boolean
  error?: string
  startedAt: string
  endedAt?: string
}

export interface ContainerView {
  id: string
  name: string
  image: string
  project?: string
  service?: string
  oneOff?: boolean
  buildable?: boolean
  /** The compose project directory, shortened with ~. */
  workingDir?: string
  state: ContainerState | string
  health?: 'healthy' | 'unhealthy' | 'starting' | string
  /** The engine's own summary, for example "Up 3 hours (healthy)". */
  status: string
  startedAt?: string
  finishedAt?: string
  exitCode?: number
  oomKilled?: boolean
  /** Exited in failure (not finished or stopped on request), or dead. */
  failed?: boolean
  restarts?: number
  ports?: ContainerPort[]
  cpu: number
  mem: number
  memLimit?: number
  cpuHistory?: number[]
  memHistory?: number[]
  /** The primary server attached by project mapping, directory or local project ID. */
  server?: string
  /** Other servers that use this container too. */
  sharedWith?: string[]
  /** Servers this container needs attention for. */
  issueFor?: string[]
  job?: ContainerJob
}

export interface DockerState {
  status: 'connecting' | 'ready' | 'unavailable'
  error?: string
  host?: string
  context?: string
  /** The product behind the endpoint: OrbStack, Docker Desktop, Colima, Rancher Desktop or Docker. */
  engine: string
  version?: string
  /** A start was requested and the engine has not answered yet. */
  starting?: boolean
  containers: ContainerView[]
}

export interface ContainerDetails {
  name: string
  id: string
  image: string
  command: string
  created: string
  restartPolicy?: string
  health?: { status: string; failingStreak: number; output?: string; exitCode: number }
  error?: string
  env: { key: string; value: string }[]
  mounts: { type: string; name?: string; source: string; destination: string; readOnly?: boolean }[]
  networks: { name: string; ip?: string; aliases?: string[] }[]
  configFiles?: string[]
}

/** A server's entry as written in the config file; durations and sizes are the text written. */
export interface Spec {
  name: string
  dir: string
  cmd: string
  /** A task that runs to completion (`oneshot = true`). */
  oneshot?: boolean
  port?: number
  group?: string
  dependsOn?: string[]
  projects?: string[]
  env?: Record<string, string>
  restart?: string
  health?: string
  readyLog?: string
  readyAfter?: string
  icon?: string
  open?: string
  stopTimeout?: string
  enabled?: boolean
  lan?: boolean
  schedule?: string
  watch?: string[]
  wrap?: string
  /** Hook commands: absent inherits the top-level one, '' runs none. */
  onCrash?: string
  onUnready?: string
  onRecover?: string
  unreadyAfter?: string
  retryAfter?: string
  umask?: string
  memoryLimit?: string
  comment?: string
}

/** The config file's top-level settings as written; '' is unset. */
export interface Settings {
  root: string
  /** 0 leaves the port to the daemon. */
  port: number
  path: string[] | null
  env: Record<string, string> | null
  restore: boolean
  notify: boolean
  stopTimeout: string
  shell: string
  dockerStart: string
  onCrash: string
  onUnready: string
  onRecover: string
  unreadyAfter: string
  retryAfter: string
  memoryLimit: string
  loginEnv: boolean
  umask: string
}

export interface SettingsView {
  values: Settings
  /** What applies to a key that is not set ('' where that is none, or the daemon's own). */
  defaults: Settings
  /** The config revision the values come from; a save is refused once the file changed since. */
  revision: string
}

export interface ListenerView extends Listener {
  cwdDisplay?: string
  server?: string
  managed: boolean
  protected?: string
  /** The container whose published port this is. */
  container?: { name: string; service?: string; server?: string }
}

export interface Suggestion {
  name: string
  dir: string
  cmd: string
  kind: string
  port?: number
  noPort?: boolean
  directoryConfigured?: boolean
  stopTimeout?: string
  dirDisplay: string
  dirConfig: string
}

export interface DiscoverResponse {
  root: string
  suggestions: Suggestion[]
}

export type OpenTarget = 'folder' | 'editor' | 'terminal' | 'config' | 'logs'

export interface AppOption {
  value: string
  label: string
}

/** The editor and terminal settings with the apps found on this computer; '' means automatic. */
export interface AppsView {
  editor: string
  terminal: string
  editors: AppOption[]
  terminals: AppOption[]
  autoEditor: string
  autoTerminal: string
}

/** One coding-agent setup step on this computer (internal/agentsetup). */
export interface AgentItem {
  /** `<agent>-mcp` or `<agent>-skill`, agent being claude or codex. */
  id: string
  state: 'ok' | 'missing' | 'outdated' | 'foreign' | 'unavailable'
  /** What applying it runs, or what to run by hand. */
  command: string
  detail?: string
}

export interface AgentsView {
  items: AgentItem[]
  /** A request to paste into a coding agent so it does the whole setup. */
  prompt: string
}

export interface AgentResult {
  id: string
  ok: boolean
  error?: string
  /** The command to run by hand after a failure. */
  command?: string
}

export type BulkAction = 'start' | 'stop' | 'restart'

export interface BulkResult {
  name: string
  error?: string
}

/** Another device of the same Tailscale user that shares its dashboard. */
export interface Peer {
  /** Tailscale host name, shown as the label; two devices may share it. */
  name: string
  /** DNS label used in *.localhost URLs. */
  label: string
  /** Identifies the peer for connect and pair. */
  dnsName: string
  os: string
  version: string
  /** A pairing with this peer is stored here; connecting needs no code. */
  paired: boolean
  /** Present only once paired. */
  servers?: { total: number; running: number }
  connected: boolean
}

export interface PeersResponse {
  tailnet: { available: boolean; error?: string; problem?: TailnetProblem; self?: string }
  peers: Peer[]
  /** A request for a coding agent that sets up Tailscale, sharing and pairing. */
  prompt?: string
}

/** Why Tailscale is unavailable: not installed, not connected, or waiting for a sign-in. */
export type TailnetProblem = 'missing' | 'stopped' | 'signedOut'

/** Sharing of this computer with the tailnet. */
export interface RemoteShare {
  enabled: boolean
  /** Reachable on the tailnet right now. */
  active: boolean
  url?: string
  error?: string
  problem?: TailnetProblem
  /** Devices paired with this computer; each has full control while sharing is on. */
  devices: PairedDevice[]
}

/** A device that paired with this computer using a pairing code. */
export interface PairedDevice {
  id: string
  name: string
  dnsName: string
  os?: string
  pairedAt: string
  /** When it last connected or disconnected; absent in records from before it was kept. */
  lastSeen?: string
  /** It has a connection open now. */
  connected?: boolean
}

/** A one-time code another device enters to pair with this computer. */
export interface PairingCode {
  code: string
  expiresAt: string
}

/** A remote port forwarded to this computer, or busy here. */
export interface Mirror {
  port: number
  /** The local port it is forwarded to, when the user chose one other than `port`. */
  local?: number
  server?: string
  container?: string
  /** `off` when the user turned its forwarding off; it is then neither bound here nor proxied. */
  state: 'mirrored' | 'busy' | 'off' | 'limit'
}

export interface RemoteConnection {
  peer: { name: string; label: string; dnsName: string; os: string }
  status: 'connecting' | 'open' | 'reconnecting'
  error?: string
  version?: string
  /** Port of the local daemon, which proxies *.<label>.localhost to the remote. */
  localPort: number
  mirrors: Mirror[]
  since: string
}

export interface RemoteOverview {
  share: RemoteShare
  connection: RemoteConnection | null
}
