import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Dev: `npm run dev` serves the UI on :5176 and proxies /api to a running panel.
// The local Docker panel is reached through Caddy on http://localhost (override with OSSR_API).
export default defineConfig({
  plugins: [react()],
  build: { outDir: 'dist', emptyOutDir: true },
  server: { port: 5176, proxy: { '/api': { target: process.env.OSSR_API || 'http://localhost', changeOrigin: true } } },
})
