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
}

export interface State {
  daemon: Health
  config: ConfigInfo
  servers: ServerView[]
  groups: string[]
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
