import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// In production we build into the Go server's embed path so the binary
// ships with the SPA and serves it from /app/. During dev, `npm run dev`
// proxies API + media calls to the Go server on :7777.
//
// emptyOutDir is OFF so a sibling .gitkeep stays put — that placeholder
// is what makes the embed-target directory trackable in git on a fresh
// clone before anyone has run a build. Vite still rewrites index.html
// and overwrites hashed assets cleanly; old hashed assets accumulate
// but never break the runtime (index.html points at the latest names).
// `make clean` removes them when needed.
export default defineConfig({
  plugins: [react()],
  base: '/app/',
  build: {
    outDir: '../internal/web/static/app',
    emptyOutDir: false,
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
