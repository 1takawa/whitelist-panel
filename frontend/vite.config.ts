import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// The production build lands in the Go package that embeds it (internal/webui/dist).
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: { outDir: '../internal/webui/dist', emptyOutDir: true },
  server: { proxy: { '/api': 'http://127.0.0.1:28270' } },
})
