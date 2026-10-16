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
  stale?: boolean
  removed?: boolean
  enabled: boolean
  dependsOn?: string[]
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
  daemon: Health
  config: ConfigInfo
  servers: ServerView[]
  groups: string[]
  docker: DockerState
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
  kind: 'recreate' | 'rebuild'
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
  /** The server whose directory is this container's compose project directory. */
  server?: string
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
  stopTimeout?: string
  dirDisplay: string
  dirConfig: string
}

export interface DiscoverResponse {
  root: string
  suggestions: Suggestion[]
}

export type OpenTarget = 'folder' | 'editor' | 'terminal' | 'config' | 'logs'

export type BulkAction = 'start' | 'stop' | 'restart'

export interface BulkResult {
  name: string
  error?: string
}
