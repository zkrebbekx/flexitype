/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5175,
    // The shopper API is public: no credential is added anywhere in this app.
    proxy: { '/api': { target: process.env.STOREFRONT_URL ?? 'http://localhost:9200', changeOrigin: true } },
  },
  test: {
    environment: 'happy-dom',
    globals: true,
    setupFiles: ['./src/__tests__/setup.ts'],
  },
})
