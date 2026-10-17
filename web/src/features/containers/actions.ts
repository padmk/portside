import { api } from '../../api/client'
import { run } from '../servers/actions'

/** Busy key of a container, shared by every control that acts on it. */
export const busyKey = (name: string) => `container:${name}`

export const containerAction = (name: string, action: 'start' | 'stop' | 'restart') =>
  run(busyKey(name), () => api.containerAction(name, action))

export const recreate = (name: string, build: boolean, pull = false) => run(busyKey(name), () => api.recreate(name, build, pull))

export const dismissJob = (name: string) => run(busyKey(name), () => api.dismissJob(name))

export const startEngine = () => run('docker:start', () => api.startEngine())

export const projectBusyKey = (project: string) => `project:${project}`

export const projectAction = (name: string, project: string, action: 'start' | 'stop' | 'down', confirm = false) =>
  run(projectBusyKey(project), () => api.projectAction(name, action, confirm))
