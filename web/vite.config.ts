import react from '@vitejs/plugin-react'
import { defineConfig, type Plugin } from 'vite'
import { loadTheme, resolveColor, over, toHex } from './scripts/theme-css.ts'
import { DEFAULT_FAMILY, FAMILIES } from './src/theme/registry.ts'

/** The product name lives here; the app, page title and storage keys read it. */
const brand = { name: 'portside', storage: 'portside' }

const target = `http://127.0.0.1:${process.env.PORTSIDE_PORT || 7980}`

/**
 * Applies the stored theme before first paint. Mirrors state/prefs.ts: family and
 * mode come from localStorage, a missing mode follows the system setting.
 */
function themeInitScript(): string {
  const map = Object.fromEntries(FAMILIES.map((f) => [f.id, { dark: f.dark, light: f.light }]))
  return `(function(){try{var F=${JSON.stringify(map)};var s=localStorage;var f=s.getItem('${brand.storage}.themeFamily');var m=s.getItem('${brand.storage}.themeMode')||s.getItem('${brand.storage}.theme');if(!F[f])f=${JSON.stringify(DEFAULT_FAMILY)};if(m!=='light'&&m!=='dark')m=matchMedia('(prefers-color-scheme: dark)').matches?'dark':'light';var r=document.documentElement;r.dataset.theme=F[f][m];r.dataset.mode=m;r.style.colorScheme=m}catch(e){}})()`
}

const SWATCH_MODULE = 'virtual:theme-swatches'

/** Exports the resolved colors the settings preview draws, read from the same CSS the app uses. */
function themeSwatches(): Plugin {
  const resolved = '\0' + SWATCH_MODULE
  return {
    name: 'theme-swatches',
    resolveId: (id) => (id === SWATCH_MODULE ? resolved : undefined),
    load(id) {
      if (id !== resolved) return undefined
      const out: Record<string, Record<string, string>> = {}
      for (const f of FAMILIES) {
        for (const mode of ['dark', 'light'] as const) {
          const themeId = f[mode]
          const vars = loadTheme(themeId, mode)
          const hex = (token: string) => toHex(over(resolveColor(vars, `var(${token})`), resolveColor(vars, 'var(--bg)')))
          out[themeId] = Object.fromEntries(
            ['bg', 'card', 'border', 'text', 'primary', 'primary-fg', 'running', 'starting', 'crashed', 'external'].map((t) => [t, hex(`--${t}`)]),
          )
        }
      }
      return `export const swatches = ${JSON.stringify(out)}`
    },
  }
}

export default defineConfig({
  plugins: [
    react(),
    themeSwatches(),
    {
      name: 'brand-html',
      transformIndexHtml: (html: string) =>
        html
          .replaceAll('%THEME_INIT%', themeInitScript())
          .replaceAll('%BRAND_NAME%', brand.name)
          .replaceAll('%BRAND_STORAGE%', brand.storage),
    },
  ],
  define: { __BRAND__: JSON.stringify(brand) },
  build: { outDir: 'dist', emptyOutDir: true, sourcemap: false, chunkSizeWarningLimit: 700 },
  server: {
    proxy: { '/api': { target, ws: true, changeOrigin: false }, '/auth': { target, changeOrigin: false } },
  },
})
