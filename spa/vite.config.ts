import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// In production we build into the Go server's embed path so the binary
// ships with the SPA and serves it from /app/. During dev, `npm run dev`
// proxies API + media calls to the Go server on :7777.
export default defineConfig({
  plugins: [react()],
  base: '/app/',
  build: {
    outDir: '../internal/web/static/app',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:7777',
      '/media': 'http://localhost:7777',
      '/static': 'http://localhost:7777',
    },
  },
})
