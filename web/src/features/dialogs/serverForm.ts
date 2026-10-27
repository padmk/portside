import type { Spec } from '../../api/types'
import { envMap, envRows, type EnvRow } from '../../ui/EnvEditor'
import { listOf, listText } from '../../ui/listText'

export type WrapChoice = 'auto' | 'none' | 'custom'
export type Hook = 'onCrash' | 'onUnready' | 'onRecover'
export const HOOKS: Hook[] = ['onCrash', 'onUnready', 'onRecover']

/** The server form's values, as text the inputs edit. */
export interface Form {
  name: string
  dir: string
  cmd: string
  oneshot: boolean
  port: string
  group: string
  dependsOn: string[]
  restart: string
  health: string
  env: EnvRow[]
  readyLog: string
  readyAfter: string
  stopTimeout: string
  retryAfter: string
  memoryLimit: string
  enabled: boolean
  schedule: string
  /** One pattern per line. */
  watch: string
  wrap: WrapChoice
  wrapCmd: string
  umask: string
  lan: boolean
  open: string
  icon: string
  /** Separated by spaces or commas. */
  projects: string
  unreadyAfter: string
  /** undefined inherits the top-level command, '' runs none for this server. */
  onCrash?: string
  onUnready?: string
  onRecover?: string
}

/**
 * The restart policy an entry gets without one, as the daemon applies it (config.DefaultRestart):
 * a task reports a failed run at once, a server comes back after a crash.
 */
export const defaultRestart = (oneshot: boolean) => (oneshot ? 'no' : 'on-failure')

export const blank: Form = {
  name: '', dir: '', cmd: '', oneshot: false, port: '', group: '', dependsOn: [], restart: defaultRestart(false), health: '', env: [],
  readyLog: '', readyAfter: '', stopTimeout: '', retryAfter: '', memoryLimit: '', enabled: true, schedule: '', watch: '',
  wrap: 'auto', wrapCmd: '', umask: '', lan: false, open: '', icon: '', projects: '', unreadyAfter: '',
}

const wrapChoice = (wrap = ''): WrapChoice => (wrap === '' || wrap === 'auto' ? 'auto' : wrap === 'none' ? 'none' : 'custom')

export const fromSpec = (s: Spec): Form => ({
  name: s.name,
  dir: s.dir,
  cmd: s.cmd,
  oneshot: !!s.oneshot,
  port: s.port === undefined ? '' : String(s.port),
  group: s.group ?? '',
  dependsOn: s.dependsOn ?? [],
  restart: s.restart || defaultRestart(!!s.oneshot),
  health: s.health ?? '',
  env: envRows(s.env),
  readyLog: s.readyLog ?? '',
  readyAfter: s.readyAfter ?? '',
  stopTimeout: s.stopTimeout ?? '',
  retryAfter: s.retryAfter ?? '',
  memoryLimit: s.memoryLimit ?? '',
  enabled: s.enabled !== false,
  schedule: s.schedule ?? '',
  watch: listText(s.watch),
  wrap: wrapChoice(s.wrap),
  wrapCmd: wrapChoice(s.wrap) === 'custom' ? (s.wrap ?? '') : '',
  umask: s.umask ?? '',
  lan: !!s.lan,
  open: s.open ?? '',
  icon: s.icon ?? '',
  projects: listText(s.projects, ' '),
  unreadyAfter: s.unreadyAfter ?? '',
  onCrash: s.onCrash,
  onUnready: s.onUnready,
  onRecover: s.onRecover,
})

/** Whether any field of the Advanced section is set, so it opens with the form. */
export const hasAdvanced = (f: Form) =>
  [f.readyLog, f.readyAfter, f.stopTimeout, f.retryAfter, f.memoryLimit, f.schedule, f.watch, f.umask, f.open, f.icon, f.projects, f.unreadyAfter].some((v) => v !== '') ||
  !f.enabled || f.lan || f.wrap !== 'auto' || HOOKS.some((h) => f[h] !== undefined)

const text = (v: string) => v.trim() || undefined
const list = (items: string[]) => (items.length ? items : undefined)

/** The spec to save: `base` (the entry as loaded) with the form's values; what does not apply to a task is left out. */
export function toSpec(f: Form, base: Spec | null): Spec {
  const task = f.oneshot
  return {
    ...base,
    name: f.name,
    dir: f.dir.trim(),
    cmd: f.cmd.trim(),
    oneshot: task || undefined,
    port: task ? (f.port === '0' ? 0 : undefined) : f.port === '' ? undefined : Number(f.port),
    group: text(f.group),
    dependsOn: list(f.dependsOn),
    restart: f.restart,
    health: task ? undefined : text(f.health),
    readyLog: task ? undefined : f.readyLog || undefined,
    readyAfter: task ? undefined : text(f.readyAfter),
    env: envMap(f.env),
    stopTimeout: text(f.stopTimeout),
    retryAfter: text(f.retryAfter),
    memoryLimit: text(f.memoryLimit),
    enabled: f.enabled,
    schedule: text(f.schedule),
    watch: list(listOf(f.watch)),
    wrap: f.wrap === 'auto' ? undefined : f.wrap === 'none' ? 'none' : text(f.wrapCmd),
    umask: text(f.umask),
    lan: f.lan || undefined,
    open: text(f.open),
    icon: text(f.icon),
    projects: list(listOf(f.projects, /[\s,]+/)),
    unreadyAfter: text(f.unreadyAfter),
    onCrash: f.onCrash?.trim(),
    onUnready: f.onUnready?.trim(),
    onRecover: f.onRecover?.trim(),
  }
}
