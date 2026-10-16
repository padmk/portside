import { api } from '../../api/client'
import { closeDialog } from '../../state/ui'
import { followLocal, followRemote } from '../../state/live'
import { run } from '../servers/actions'

/** Connects this computer to a paired peer, by DNS name, and switches the dashboard to it. Throws the daemon's error. */
export async function connectTo(peer: string) {
  const conn = await api.connect(peer)
  closeDialog()
  followRemote(conn)
}

/** Pairs with a peer using the code it issued, then switches to it as connectTo does. */
export async function pairWith(peer: string, code: string) {
  const conn = await api.pair(peer, code)
  closeDialog()
  followRemote(conn)
}

export const disconnectRemote = () =>
  run('remote:disconnect', async () => {
    await api.disconnect()
    followLocal()
  })
