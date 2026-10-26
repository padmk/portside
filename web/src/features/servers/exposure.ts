import type { Key } from '../../i18n/dictionary'

/** How to bind a listener to loopback: an explanation and the code to use. */
export interface FixHint {
  text: Key
  code?: string
}

/** The fix for a server's command, by the tool it runs. */
export function serverFix(cmd: string, port: number): FixHint {
  const c = cmd.toLowerCase()
  if (/\b(docker[- ]compose|podman[- ]compose)\b/.test(c)) return composeFix(port, port)
  if (/\bvite\b/.test(c)) return { text: 'network.fix.vite', code: '--host 127.0.0.1' }
  if (/\bnext\b/.test(c)) return { text: 'network.fix.next', code: 'next dev --hostname 127.0.0.1' }
  if (/\bgo run\b|\bair\b/.test(c)) return { text: 'network.fix.go', code: port > 0 ? `"127.0.0.1:${port}"` : '"127.0.0.1:8080"' }
  if (/\bpython3?\b|\buv run\b|\bflask\b|\buvicorn\b|\bmanage\.py\b/.test(c)) return { text: 'network.fix.python', code: '--bind 127.0.0.1' }
  if (/\b(node|npm|pnpm|yarn|bun|deno|nodemon|tsx)\b/.test(c)) return { text: 'network.fix.node', code: "app.listen(port, '127.0.0.1')" }
  return { text: 'network.fix.other' }
}

/** The fix for a published container port: publish it on loopback only. */
export function composeFix(published: number, container: number): FixHint {
  return { text: 'network.fix.compose', code: `"127.0.0.1:${published > 0 ? published : 5432}:${container > 0 ? container : 5432}"` }
}
