import type { ServerView } from '../../api/types'
import { displayStatus, type DisplayStatus } from '../../ui/Lamp'

export type StatusGroupId = 'running' | 'starting' | 'crashed' | 'external' | 'stopped'

export const STATUS_GROUPS: { id: StatusGroupId; members: DisplayStatus[]; lamp: DisplayStatus }[] = [
  { id: 'running', members: ['running'], lamp: 'running' },
  { id: 'starting', members: ['starting', 'stopping'], lamp: 'starting' },
  { id: 'crashed', members: ['crashed', 'blocked'], lamp: 'crashed' },
  { id: 'external', members: ['external'], lamp: 'external' },
  { id: 'stopped', members: ['stopped', 'exited'], lamp: 'stopped' },
]

export const groupOf = (v: ServerView): StatusGroupId =>
  STATUS_GROUPS.find((g) => g.members.includes(displayStatus(v)))?.id ?? 'stopped'

export function countsOf(servers: ServerView[]): Record<StatusGroupId, number> {
  const out = { running: 0, starting: 0, crashed: 0, external: 0, stopped: 0 }
  for (const v of servers) out[groupOf(v)]++
  return out
}
