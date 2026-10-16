// Drives headless Chrome over the DevTools protocol to capture the dashboard.
// Usage: node scripts/screenshots.mjs <demo dir> <output dir>
import { spawn } from 'node:child_process'
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const [demo, out, set = 'readme'] = process.argv.slice(2)
const BASE = 'http://127.0.0.1:7990'
const CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const token = readFileSync(join(demo, 'state', 'auth-token'), 'utf8').trim()

async function signInLink(next = '/') {
  const res = await fetch(`${BASE}/api/auth/code`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({ next }),
  })
  return (await res.json()).url
}

const port = 9444
const chrome = spawn(CHROME, [`--remote-debugging-port=${port}`, `--user-data-dir=${mkdtempSync(join(tmpdir(), 'ps-chrome-'))}`,
  // The mock keychain keeps the throwaway profile away from macOS keychains, so no keychain prompt appears.
  '--headless=new', '--use-mock-keychain', '--no-first-run', '--disable-gpu', '--hide-scrollbars', 'about:blank'], { stdio: 'ignore' })
let targets = []
for (let i = 0; i < 50 && !targets.length; i++) {
  try { targets = (await (await fetch(`http://127.0.0.1:${port}/json`)).json()).filter((t) => t.type === 'page') } catch {}
  await sleep(200)
}
const ws = new WebSocket(targets[0].webSocketDebuggerUrl)
await new Promise((r) => (ws.onopen = r))
let id = 0
const pending = new Map()
ws.onmessage = (ev) => {
  const m = JSON.parse(ev.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
}
const send = (method, params = {}) => new Promise((res) => { const i = ++id; pending.set(i, res); ws.send(JSON.stringify({ id: i, method, params })) })
const evaluate = (expression) => Promise.race([
  send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true }),
  sleep(5000).then(() => ({ result: { value: 'timeout' } })),
])
const shot = async (name) => {
  const r = await send('Page.captureScreenshot', { format: 'png' })
  writeFileSync(join(out, name), Buffer.from(r.data, 'base64'))
  console.log('wrote', name)
}
const locale = set === 'portfolio' ? 'pl' : 'en'
const theme = async (family, mode) => {
  await evaluate(`localStorage.setItem('portside.themeFamily', ${JSON.stringify(family)}); localStorage.setItem('portside.themeMode', ${JSON.stringify(mode)}); localStorage.setItem('portside.locale', ${JSON.stringify(locale)})`)
}
let loads = 0
// A query string forces a real reload so the stored theme is applied.
const open = async (hash = '', wait = 2500) => {
  await send('Page.navigate', { url: `${BASE}/?shot=${++loads}${hash}` })
  await sleep(wait)
}

await send('Page.enable')
await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 2, mobile: false })
const link = await signInLink('/')
await send('Page.navigate', { url: link })
await sleep(2000)

const session = (await evaluate("fetch('/api/session').then(r => r.json()).then(j => j.authenticated)")).result.value
if (session !== true) {
  console.error('the browser is not signed in:', session)
  chrome.kill()
  process.exit(1)
}
if (set === 'portfolio') {
  // Pairs for a site that swaps images with its own light and dark theme.
  for (const [mode, suffix] of [['dark', ''], ['light', '-light']]) {
    await theme('rose-pine', mode)
    // Cards with the stopped shop group collapsed, so one screen shows every state.
    await evaluate(`localStorage.removeItem('portside.layout'); localStorage.setItem('portside.collapsed', '["shop"]')`)
    await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1080, deviceScaleFactor: 2, mobile: false })
    await open(); await shot(`portside-overview${suffix}.png`)
    await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 2, mobile: false })
    await evaluate("localStorage.setItem('portside.layout', 'list'); localStorage.removeItem('portside.collapsed')")
    await open(); await shot(`portside-list${suffix}.png`)
    await evaluate("localStorage.removeItem('portside.layout')")
    await open('#/web', 3500); await shot(`portside-detail${suffix}.png`)
    await open('#/blog', 3000); await shot(`portside-crashed${suffix}.png`)
  }
} else {
  await evaluate(`localStorage.setItem('portside.collapsed', '["shop"]')`)
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1080, deviceScaleFactor: 2, mobile: false })
  await theme('rose-pine', 'dark'); await open(); await shot('overview-dark.png')
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 2, mobile: false })
  await evaluate("localStorage.setItem('portside.layout', 'list'); localStorage.removeItem('portside.collapsed')")
  await theme('everforest', 'light'); await open(); await shot('list.png')
  await evaluate("localStorage.removeItem('portside.layout')")
  await theme('tokyo-night', 'dark'); await open('#/web', 3500); await shot('detail.png')
  await theme('catppuccin', 'light'); await open('#/blog', 3000); await shot('crashed.png')
}

await send('Browser.close').catch(() => {})
chrome.kill()
process.exit(0)
