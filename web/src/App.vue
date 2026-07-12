<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, RouterView } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { Shapes, Boxes, ScrollText, Radio, Settings, Moon, Sun, Braces, LogOut } from 'lucide-vue-next'
import { useTheme } from '@/composables/useTheme'
import Toasts from '@/components/ui/Toasts.vue'
import { authRequired, isAuthenticated, setToken, signOut, bearerHeader, challenge } from '@/lib/auth'

const { theme, toggle } = useTheme()

// Bearer-token sign-in. In development / playground mode the service runs with
// auth disabled, so nothing 401s and this overlay never appears.
const tokenInput = ref('')
function submitToken() {
  const t = tokenInput.value.trim()
  if (!t) return
  setToken(t)
  location.reload() // re-run every query with the token attached
}
function doSignOut() {
  signOut()
  location.reload()
}

// Playground builds run the service in-browser; data resets on reload.
const isPlayground = import.meta.env.VITE_WASM === '1'
const baseUrl = import.meta.env.BASE_URL

const features = useQuery({
  queryKey: ['features'],
  queryFn: async () => {
    const r = await fetch('/api/v1/features', { headers: bearerHeader() })
    if (r.status === 401) challenge()
    return r.json() as Promise<{ search: boolean; activity: boolean; event_delivery?: boolean }>
  },
  staleTime: Infinity,
  retry: false,
})

const nav = computed(() => [
  { to: '/types', label: 'Types', icon: Shapes },
  { to: '/entities', label: 'Entities', icon: Boxes },
  { to: '/graphql', label: 'GraphQL', icon: Braces },
  ...(features.data.value?.event_delivery ? [{ to: '/delivery', label: 'Delivery', icon: Radio }] : []),
  ...(features.data.value?.activity === false ? [] : [{ to: '/activity', label: 'Activity', icon: ScrollText }]),
  { to: '/settings', label: 'Settings', icon: Settings },
])

const health = useQuery({
  queryKey: ['health'],
  queryFn: async () => {
    const res = await fetch('/readyz')
    return { ok: res.ok, ...(await res.json()) } as { ok: boolean; version?: string }
  },
  refetchInterval: 30_000,
  retry: false,
})
</script>

<template>
  <div class="flex h-full">
    <aside class="flex w-52 shrink-0 flex-col border-r border-(--border) bg-(--surface)">
      <div class="flex items-center gap-2 px-4 py-4">
        <img :src="`${baseUrl}logo.svg`" alt="" class="h-7 w-7" />
        <span class="text-[15px] font-semibold tracking-tight">flexitype</span>
        <span
          v-if="isPlayground"
          class="rounded-full bg-(--accent-soft) px-1.5 py-0.5 text-[10px] font-semibold text-(--accent)"
          title="The full service runs in your browser via WebAssembly. Data resets on reload."
          >demo</span
        >
      </div>

      <nav class="flex flex-1 flex-col gap-0.5 px-2">
        <RouterLink
          v-for="item in nav"
          :key="item.to"
          :to="item.to"
          class="flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-sm font-medium text-(--text-secondary) hover:bg-(--canvas) hover:text-(--text)"
          active-class="!bg-(--accent-soft) !text-(--accent)"
        >
          <component :is="item.icon" :size="16" />
          {{ item.label }}
        </RouterLink>
      </nav>

      <footer class="flex items-center justify-between border-t border-(--border) px-4 py-3">
        <span class="flex items-center gap-1.5 text-[12px] text-(--text-muted)">
          <span
            class="h-2 w-2 rounded-full"
            :class="health.data.value?.ok ? 'bg-(--ok)' : 'bg-(--danger)'"
            :title="health.data.value?.ok ? 'Service healthy' : 'Service unreachable'"
          />
          {{ health.data.value?.version ?? '—' }}
        </span>
        <div class="flex items-center gap-1">
          <button
            v-if="isAuthenticated"
            class="rounded-md p-1.5 text-(--text-muted) hover:bg-(--canvas) hover:text-(--text)"
            aria-label="Sign out"
            title="Sign out"
            @click="doSignOut"
          >
            <LogOut :size="16" />
          </button>
          <button
            class="rounded-md p-1.5 text-(--text-muted) hover:bg-(--canvas) hover:text-(--text)"
            :aria-label="theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'"
            @click="toggle"
          >
            <Sun v-if="theme === 'dark'" :size="16" />
            <Moon v-else :size="16" />
          </button>
        </div>
      </footer>
    </aside>

    <main class="flex-1 overflow-y-auto">
      <div class="mx-auto max-w-5xl px-6 py-6">
        <RouterView />
      </div>
    </main>

    <Toasts />

    <!-- Sign-in overlay: shown when the service rejects a request for auth. -->
    <div
      v-if="authRequired"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      role="dialog"
      aria-modal="true"
      aria-labelledby="signin-title"
    >
      <form
        class="w-full max-w-sm rounded-lg border border-(--border) bg-(--surface) p-6 shadow-xl"
        @submit.prevent="submitToken"
      >
        <h2 id="signin-title" class="text-base font-semibold">Authentication required</h2>
        <p class="mt-1.5 text-sm text-(--text-secondary)">
          This deployment requires a service-account token. Paste a bearer token
          (<code class="text-(--text-muted)">ft_&lt;account&gt;_&lt;secret&gt;</code>) to continue.
        </p>
        <input
          v-model="tokenInput"
          type="password"
          autocomplete="off"
          placeholder="ft_…"
          aria-label="Service-account token"
          class="mt-4 w-full rounded-md border border-(--border) bg-(--canvas) px-3 py-2 text-sm outline-none focus:border-(--accent)"
        />
        <button
          type="submit"
          class="mt-4 w-full rounded-md bg-(--accent) px-3 py-2 text-sm font-medium text-white hover:opacity-90 disabled:opacity-50"
          :disabled="!tokenInput.trim()"
        >
          Sign in
        </button>
      </form>
    </div>
  </div>
</template>
