import { createHash } from 'node:crypto'
import { readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'

const api = process.env.RITM_API ?? 'http://127.0.0.1:7486'

// swVersion stamps dist/sw.js with a hash of the bundle and the cached static files, so every
// deploy that changes them ships a new service worker, and its cache name changes with it.
// It also puts back dist/.gitkeep, which emptyOutDir removes: go:embed needs a file in a fresh checkout.
function swVersion(): Plugin {
  let outDir = 'dist'
  let names: string[] = []
  return {
    name: 'ritm-sw-version',
    apply: 'build',
    configResolved(c) {
      outDir = c.build.outDir
    },
    generateBundle(_, bundle) {
      names = Object.keys(bundle).sort()
    },
    closeBundle() {
      writeFileSync(join(outDir, '.gitkeep'), '')
      const sw = join(outDir, 'sw.js')
      const h = createHash('sha256').update(names.join('\n'))
      for (const f of ['offline.html', 'manifest.webmanifest']) h.update(readFileSync(join(outDir, f)))
      const hash = h.digest('hex').slice(0, 12)
      writeFileSync(sw, readFileSync(sw, 'utf8').replaceAll('__BUILD__', hash))
    },
  }
}

// `vite dev` proxies the Go server; `vite build` writes web/dist, embedded by go:embed.
export default defineConfig({
  plugins: [vue(), swVersion()],
  server: {
    port: 5173,
    host: true,
    proxy: {
      '/api': { target: api, changeOrigin: false },
      '/mcp': { target: api, changeOrigin: false },
      '/healthz': { target: api, changeOrigin: false },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'es2022',
    chunkSizeWarningLimit: 800,
  },
})
