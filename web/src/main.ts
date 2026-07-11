import { createApp } from 'vue'
import { VueQueryPlugin } from '@tanstack/vue-query'
import App from './App.vue'
import { router } from './router'
import './styles/main.css'

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
