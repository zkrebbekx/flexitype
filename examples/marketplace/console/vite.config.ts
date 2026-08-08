/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    // The SDK is a LINKED dependency (`file:../../../client-ts`), so it brings
    // its own node_modules with it. React and TanStack Query are peer
    // dependencies of the SDK, and without this the production build resolves
    // the SDK's copy while the app uses its own. Two copies of TanStack Query
    // means two React contexts: every SDK hook then throws "No QueryClient
    // set" although the app has a provider. The development server dedupes
    // this by itself, so the failure only appeared in the built console.
    dedupe: ['react', 'react-dom', '@tanstack/react-query'],
  },
  server: {
    port: 5174,
    // `npm run dev` talks to the platform directly. In the compose stack the
    // console is served by nginx, which proxies /api and adds the credential.
    proxy: {
      '/api': {
        target: process.env.PLATFORM_URL ?? 'http://localhost:9300',
        changeOrigin: true,
        configure(proxy) {
          // The credential is added by the PROXY, exactly as nginx does it in
          // the compose stack, so `npm run dev` and the built console behave
          // the same way and neither puts a token in the browser.
          proxy.on('proxyReq', (request) => {
            const token = process.env.PLATFORM_API_TOKEN
            if (token !== undefined && token !== '') request.setHeader('Authorization', `Bearer ${token}`)
          })
        },
      },
    },
  },
  test: {
    environment: 'happy-dom',
    globals: true,
    setupFiles: ['./src/__tests__/setup.ts'],
  },
})
