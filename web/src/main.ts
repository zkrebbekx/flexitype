import { createApp } from 'vue'
import { VueQueryPlugin } from '@tanstack/vue-query'
import App from './App.vue'
import { router } from './router'
import './styles/main.css'

async function boot() {
  // Playground builds run the whole service in-browser via WebAssembly;
  // the app must not mount until the fetch shim is in place.
  if (import.meta.env.VITE_WASM === '1') {
    const { bootWasm } = await import('./lib/wasm')
    await bootWasm()
  }

  createApp(App)
    .use(router)
    .use(VueQueryPlugin, {
      queryClientConfig: {
        defaultOptions: {
          queries: { retry: 1, staleTime: 15_000, refetchOnWindowFocus: false },
        },
      },
    })
    .mount('#app')
}

void boot()
