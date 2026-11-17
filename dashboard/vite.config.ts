/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// In development, run `zerosetup dashboard --port 4316 --no-open` and open
// http://localhost:5173/#token=<token>. The proxy presents same-origin
// headers so the API's Host/Origin checks pass.
const api = 'http://127.0.0.1:4316'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: { '/api': { target: api, changeOrigin: true, headers: { origin: api } } },
  },
  build: { outDir: 'dist', sourcemap: false, chunkSizeWarningLimit: 900 },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
  },
})
