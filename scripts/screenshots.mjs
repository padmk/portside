// Drives headless Chrome over the DevTools protocol to capture the dashboard.
// Usage: node scripts/screenshots.mjs <demo dir> <output dir>
import { spawn } from 'node:child_process'
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const [demo, out, set = 'readme', port = '7990'] = process.argv.slice(2)
// Direct API calls go to the loopback address; the browser uses the dashboard's
// own host, taken from the sign-in link.
const API = `http://127.0.0.1:${port}`
const CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const token = readFileSync(join(demo, 'state', 'auth-token'), 'utf8').trim()

async function signInLink(next = '/') {
  const res = await fetch(`${API}/api/auth/code`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({ next }),
  })
  return res.json()
}

const debugPort = 9444
const chrome = spawn(CHROME, [`--remote-debugging-port=${debugPort}`, `--user-data-dir=${mkdtempSync(join(tmpdir(), 'ps-chrome-'))}`,
  // The mock keychain keeps the throwaway profile away from macOS keychains, so no keychain prompt appears.
  '--headless=new', '--use-mock-keychain', '--no-first-run', '--disable-gpu', '--hide-scrollbars', 'about:blank'], { stdio: 'ignore' })
let targets = []
for (let i = 0; i < 50 && !targets.length; i++) {
  try { targets = (await (await fetch(`http://127.0.0.1:${debugPort}/json`)).json()).filter((t) => t.type === 'page') } catch {}
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
  await send('Page.navigate', { url: `${dashboard}/?shot=${++loads}${hash}` })
  await sleep(wait)
}

// A server's Containers tab, with its first container's logs.
const containersTab = async (name) => {
  await open(`#/${name}`, 2500)
  await evaluate(`[...document.querySelectorAll('[role=tab]')].find((b) => /Contain|Konten/.test(b.textContent))?.click()`)
  await sleep(3000)
}

// The Settings dialog (opened with the g s shortcut), scrolled to the editor and terminal pickers.
const settingsDialog = async () => {
  await open()
  await evaluate(`for (const key of ['g', 's']) document.body.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }))`)
  await sleep(1500)
}

// Settings on the coding agents, as the menu bar app's Connect Coding Agents opens it.
const agentsDialog = async () => {
  await open('#settings/agents', 3000)
}

await send('Page.enable')
await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 2, mobile: false })
// The link is on 127.0.0.1 and forwards to the dashboard host, which the reply names.
const { url: link, dashboard } = await signInLink('/')
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
    await containersTab('api'); await shot(`portside-containers${suffix}.png`)
    await settingsDialog(); await shot(`portside-settings${suffix}.png`)
    await agentsDialog(); await shot(`portside-agents${suffix}.png`)
  }
} else {
  await evaluate(`localStorage.setItem('portside.collapsed', '["shop"]')`)
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1080, deviceScaleFactor: 2, mobile: false })
  await theme('rose-pine', 'dark'); await open(); await shot('overview-dark.png')
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 2, mobile: false })
  await evaluate("localStorage.setItem('portside.layout', 'list'); localStorage.removeItem('portside.collapsed')")
  await theme('rose-pine', 'dark'); await open(); await shot('list.png')
  await evaluate("localStorage.removeItem('portside.layout')")
  await theme('rose-pine', 'dark'); await open('#/web', 3500); await shot('detail.png')
  await theme('rose-pine', 'dark'); await open('#/blog', 3000); await shot('crashed.png')
  await theme('rose-pine', 'dark'); await containersTab('api'); await shot('containers.png')
  await theme('rose-pine', 'dark'); await settingsDialog(); await shot('settings.png')
  await theme('rose-pine', 'dark'); await agentsDialog(); await shot('agents.png')
}

await send('Browser.close').catch(() => {})
chrome.kill()
process.exit(0)
