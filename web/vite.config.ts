import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

const api = process.env.RITM_API ?? 'http://127.0.0.1:8080'

// `vite dev` proxies the Go server (FE-12); `vite build` writes web/dist, embedded by go:embed.
export default defineConfig({
  plugins: [vue()],
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
