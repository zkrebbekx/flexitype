<script setup lang="ts">
import { computed } from 'vue'
import { useQuery, useMutation } from '@tanstack/vue-query'
import { api, friendlyError } from '@/lib/api'
import { useToasts } from '@/composables/useToasts'
import PageHeader from '@/components/ui/PageHeader.vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import ErrorState from '@/components/ui/ErrorState.vue'
import { RefreshCw } from 'lucide-vue-next'

const toasts = useToasts()

const features = useQuery({ queryKey: ['features'], queryFn: () => api.getFeatures() })

const health = useQuery({
  queryKey: ['health'],
  queryFn: async () => {
    const res = await fetch('/readyz')
    return (await res.json()) as { status?: string; version?: string }
  },
})

const flags = computed(() => {
  const f = features.data.value
  if (!f) return []
  return [
    { key: 'search', label: 'FQL search', on: f.search, hint: 'The query language surface (/query).' },
    { key: 'activity', label: 'Activity log', on: f.activity, hint: 'Audit history of every change.' },
    { key: 'search_index', label: 'Search index', on: f.search_index, hint: 'Full-text projection powering FQL matches().' },
    { key: 'event_delivery', label: 'Event delivery', on: f.event_delivery, hint: 'Webhook subscriptions and the events feed (requires the outbox).' },
  ]
})

const reindex = useMutation({
  mutationFn: () => api.reindexSearch(),
  onSuccess: (r) => toasts.success(`Reindexed ${r.reindexed} entit${r.reindexed === 1 ? 'y' : 'ies'}`),
  onError: (e) => toasts.error(friendlyError(e)),
})
</script>

<template>
  <PageHeader title="Settings" :crumbs="[{ label: 'Settings' }]">
    Deployment capabilities and operational actions for this instance.
  </PageHeader>

  <ErrorState v-if="features.isError.value" :error="features.error.value" @retry="features.refetch()" />

  <template v-else>
    <!-- Deployment -->
    <section class="mt-4 rounded-lg border border-(--border) bg-(--surface) p-4">
      <h2 class="text-sm font-semibold">Deployment</h2>
      <dl class="mt-3 grid grid-cols-[auto_1fr] gap-x-6 gap-y-1.5 text-sm">
        <dt class="text-(--text-muted)">Version</dt>
        <dd class="mono">{{ health.data.value?.version ?? '—' }}</dd>
        <dt class="text-(--text-muted)">Status</dt>
        <dd>
          <Badge :tone="health.data.value?.status === 'ok' || health.isSuccess.value ? 'ok' : 'warn'">
            {{ health.data.value?.status ?? (health.isSuccess.value ? 'ready' : '…') }}
          </Badge>
        </dd>
      </dl>
    </section>

    <!-- Feature flags -->
    <section class="mt-4 rounded-lg border border-(--border) bg-(--surface) p-4">
      <h2 class="text-sm font-semibold">Features</h2>
      <p class="mt-0.5 text-[13px] text-(--text-muted)">
        Read-only — these are configured at deploy time via environment variables.
      </p>
      <ul class="mt-3 flex flex-col divide-y divide-(--border)">
        <li v-for="f in flags" :key="f.key" class="flex items-center justify-between gap-4 py-2.5">
          <div class="text-sm">
            <p class="font-medium">{{ f.label }}</p>
            <p class="text-[13px] text-(--text-muted)">{{ f.hint }}</p>
          </div>
          <Badge :tone="f.on ? 'ok' : 'neutral'">{{ f.on ? 'enabled' : 'disabled' }}</Badge>
        </li>
      </ul>
    </section>

    <!-- Operations -->
    <section class="mt-4 rounded-lg border border-(--border) bg-(--surface) p-4">
      <h2 class="text-sm font-semibold">Operations</h2>
      <div class="mt-3 flex items-center justify-between gap-4">
        <div class="text-sm">
          <p class="font-medium">Reindex search</p>
          <p class="text-[13px] text-(--text-muted)">
            Rebuild the full-text projection for this tenant — use after a bulk import or if results look stale.
          </p>
        </div>
        <Button
          v-if="features.data.value?.search_index"
          variant="primary"
          size="sm"
          :disabled="reindex.isPending.value"
          @click="reindex.mutate()"
        >
          <RefreshCw :size="14" :class="reindex.isPending.value ? 'animate-spin' : ''" />
          {{ reindex.isPending.value ? 'Reindexing…' : 'Reindex' }}
        </Button>
        <Badge v-else tone="neutral">search index disabled</Badge>
      </div>
    </section>
  </template>
</template>
