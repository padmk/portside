export type Status = 'stopped' | 'starting' | 'running' | 'stopping' | 'crashed' | 'exited' | 'blocked'

export type RestartPolicy = 'no' | 'on-failure' | 'always'

export interface Listener {
  port: number
  pid: number
  command: string
  cwd?: string
  address?: string
}

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
  /** Ports the process tree listens on, when they differ from the configured port. */
  listenPorts?: number[]
  /** A copy of the configured server `origin` running in the checkout `worktree`; not in the config file. */
  temporary?: boolean
  origin?: string
  worktree?: string
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
}

export type ContainerState = 'created' | 'running' | 'paused' | 'restarting' | 'removing' | 'exited' | 'dead'

export interface ContainerPort {
  private: number
  public: number
  proto: string
  ip?: string
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

export interface Spec {
  name: string
  dir: string
  cmd: string
  port?: number
  group?: string
  dependsOn?: string[]
  projects?: string[]
  env?: Record<string, string>
  restart?: string
  health?: string
  icon?: string
  open?: string
  stopTimeout?: string
  enabled?: boolean
  comment?: string
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
  state: 'mirrored' | 'busy'
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
