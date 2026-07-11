<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, RouterView } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { Shapes, Boxes, ScrollText, Radio, Moon, Sun } from 'lucide-vue-next'
import { useTheme } from '@/composables/useTheme'
import Toasts from '@/components/ui/Toasts.vue'

const { theme, toggle } = useTheme()

// Playground builds run the service in-browser; data resets on reload.
const isPlayground = import.meta.env.VITE_WASM === '1'
const baseUrl = import.meta.env.BASE_URL

const features = useQuery({
  queryKey: ['features'],
  queryFn: () =>
    fetch('/api/v1/features').then(
      (r) => r.json() as Promise<{ search: boolean; activity: boolean; event_delivery?: boolean }>,
    ),
  staleTime: Infinity,
  retry: false,
})

const nav = computed(() => [
  { to: '/types', label: 'Types', icon: Shapes },
  { to: '/entities', label: 'Entities', icon: Boxes },
  ...(features.data.value?.event_delivery ? [{ to: '/delivery', label: 'Delivery', icon: Radio }] : []),
  ...(features.data.value?.activity === false ? [] : [{ to: '/activity', label: 'Activity', icon: ScrollText }]),
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
        <button
          class="rounded-md p-1.5 text-(--text-muted) hover:bg-(--canvas) hover:text-(--text)"
          :aria-label="theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'"
          @click="toggle"
        >
          <Sun v-if="theme === 'dark'" :size="16" />
          <Moon v-else :size="16" />
        </button>
      </footer>
    </aside>

    <main class="flex-1 overflow-y-auto">
      <div class="mx-auto max-w-5xl px-6 py-6">
        <RouterView />
      </div>
    </main>

    <Toasts />
  </div>
</template>
