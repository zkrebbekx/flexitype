<script setup lang="ts">
import { computed, ref } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { api, friendlyError } from '@/lib/api'
import type { DeliveryStatus, WebhookSubscription } from '@/lib/api'
import { formatRelative } from '@/lib/format'
import { useToasts } from '@/composables/useToasts'
import PageHeader from '@/components/ui/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import Badge from '@/components/ui/Badge.vue'
import Select from '@/components/ui/Select.vue'
import Tabs from '@/components/ui/Tabs.vue'
import Modal from '@/components/ui/Modal.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import ErrorState from '@/components/ui/ErrorState.vue'
import SkeletonRows from '@/components/ui/SkeletonRows.vue'
import WebhookSubscriptionDrawer from '@/components/WebhookSubscriptionDrawer.vue'
import { Plus, RefreshCw, Trash2, Pencil } from 'lucide-vue-next'

const toasts = useToasts()
const queryClient = useQueryClient()

const tab = ref('subscriptions')
const tabs = [
  { key: 'subscriptions', label: 'Subscriptions' },
  { key: 'events', label: 'Events feed' },
]

// --- subscriptions ------------------------------------------------------------
const subscriptions = useQuery({
  queryKey: ['webhook-subscriptions'],
  queryFn: () => api.listSubscriptions(),
})

const drawer = ref(false)
const editing = ref<WebhookSubscription>()
function openDrawer(s?: WebhookSubscription) {
  editing.value = s
  drawer.value = true
}

const confirmDelete = ref<WebhookSubscription>()
const remove = useMutation({
  mutationFn: (s: WebhookSubscription) => api.deleteSubscription(s.id),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['webhook-subscriptions'] })
    toasts.success('Subscription deleted')
    confirmDelete.value = undefined
    if (selectedId.value && !subscriptions.data.value?.items.some((s) => s.id === selectedId.value)) {
      selectedId.value = ''
    }
  },
  onError: (e) => toasts.error(friendlyError(e)),
})

// --- deliveries ---------------------------------------------------------------
const selectedId = ref('')
const statusFilter = ref<DeliveryStatus | ''>('')
const STATUS_TONES: Record<DeliveryStatus, 'ok' | 'accent' | 'warn' | 'danger'> = {
  delivered: 'ok',
  pending: 'accent',
  inflight: 'accent',
  dead: 'danger',
}
const STATUS_OPTIONS = [
  { value: '', label: 'All statuses' },
  { value: 'pending', label: 'Pending' },
  { value: 'inflight', label: 'In-flight' },
  { value: 'delivered', label: 'Delivered' },
  { value: 'dead', label: 'Dead-lettered' },
]

const deliveries = useQuery({
  queryKey: ['webhook-deliveries', selectedId, statusFilter],
  queryFn: () =>
    api.listDeliveries(selectedId.value, {
      status: statusFilter.value || undefined,
      limit: 50,
    }),
  enabled: computed(() => !!selectedId.value),
})

const redeliver = useMutation({
  mutationFn: (deliveryId: string) => api.redeliver(deliveryId),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['webhook-deliveries'] })
    toasts.success('Delivery requeued')
  },
  onError: (e) => toasts.error(friendlyError(e)),
})

function subscriptionName(id: string): string {
  return subscriptions.data.value?.items.find((s) => s.id === id)?.name ?? id
}

// --- events feed --------------------------------------------------------------
const events = useQuery({
  queryKey: ['events-feed'],
  queryFn: () => api.listEvents({ limit: 50 }),
  enabled: computed(() => tab.value === 'events'),
  refetchInterval: 5_000,
})
</script>

<template>
  <PageHeader title="Delivery">
    Route events to other services: register webhook subscriptions, watch deliveries, and browse the event feed.
    <template #actions>
      <Button variant="primary" @click="openDrawer()"><Plus :size="15" /> New subscription</Button>
    </template>
  </PageHeader>

  <Tabs v-model="tab" :tabs="tabs" />

  <!-- Subscriptions -->
  <section v-if="tab === 'subscriptions'" class="mt-4 flex flex-col gap-4">
    <ErrorState
      v-if="subscriptions.isError.value"
      :error="subscriptions.error.value"
      @retry="subscriptions.refetch()"
    />
    <template v-else>
      <div class="overflow-hidden rounded-lg border border-(--border) bg-(--surface)">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-(--border) bg-(--canvas) text-left text-[13px] text-(--text-muted)">
              <th class="px-3 py-2 font-medium">Name</th>
              <th class="px-3 py-2 font-medium">URL</th>
              <th class="px-3 py-2 font-medium">Events</th>
              <th class="px-3 py-2 font-medium">Status</th>
              <th class="px-3 py-2"></th>
            </tr>
          </thead>
          <tbody>
            <SkeletonRows v-if="subscriptions.isPending.value" :rows="3" :cols="5" />
            <tr
              v-for="s in subscriptions.data.value?.items"
              v-else
              :key="s.id"
              class="cursor-pointer border-b border-(--border) last:border-0 hover:bg-(--canvas)"
              :class="{ 'bg-(--canvas)': selectedId === s.id }"
              @click="selectedId = s.id"
            >
              <td class="px-3 py-2.5 font-medium">{{ s.name }}</td>
              <td class="mono px-3 py-2.5 text-[12.5px] text-(--text-secondary)">{{ s.url }}</td>
              <td class="px-3 py-2.5 text-(--text-muted)">
                {{ s.event_types.length ? s.event_types.length + ' type(s)' : 'all' }}
              </td>
              <td class="px-3 py-2.5">
                <Badge :tone="s.active ? 'ok' : 'neutral'">{{ s.active ? 'active' : 'inactive' }}</Badge>
              </td>
              <td class="px-3 py-2.5 text-right whitespace-nowrap">
                <Button size="sm" variant="ghost" aria-label="Edit" @click.stop="openDrawer(s)"><Pencil :size="14" /></Button>
                <Button size="sm" variant="ghost" aria-label="Delete" @click.stop="confirmDelete = s"><Trash2 :size="14" /></Button>
              </td>
            </tr>
          </tbody>
        </table>
        <EmptyState
          v-if="!subscriptions.isPending.value && !subscriptions.data.value?.items.length"
          title="No subscriptions yet"
          body="Register an endpoint to receive events. Deliveries are signed, retried and dead-lettered automatically."
          class="m-4"
        />
      </div>

      <!-- Delivery log for the selected subscription -->
      <div v-if="selectedId" class="flex flex-col gap-2">
        <div class="flex items-center justify-between">
          <h2 class="text-sm font-semibold">
            Deliveries · <span class="text-(--text-secondary)">{{ subscriptionName(selectedId) }}</span>
          </h2>
          <Select v-model="statusFilter" label="" :options="STATUS_OPTIONS" class="w-44" />
        </div>
        <ErrorState v-if="deliveries.isError.value" :error="deliveries.error.value" @retry="deliveries.refetch()" />
        <div v-else class="overflow-hidden rounded-lg border border-(--border) bg-(--surface)">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-(--border) bg-(--canvas) text-left text-[13px] text-(--text-muted)">
                <th class="px-3 py-2 font-medium">Event</th>
                <th class="px-3 py-2 font-medium">Status</th>
                <th class="px-3 py-2 font-medium">Attempts</th>
                <th class="px-3 py-2 font-medium">Last change</th>
                <th class="px-3 py-2"></th>
              </tr>
            </thead>
            <tbody>
              <SkeletonRows v-if="deliveries.isPending.value" :rows="4" :cols="5" />
              <tr v-for="d in deliveries.data.value?.items" v-else :key="d.id" class="border-b border-(--border) last:border-0">
                <td class="px-3 py-2.5">
                  <span class="text-(--text-secondary)">{{ d.event_type }}</span>
                  <span v-if="d.last_error" class="mt-0.5 block text-[12px] text-(--danger)">{{ d.last_error }}</span>
                </td>
                <td class="px-3 py-2.5"><Badge :tone="STATUS_TONES[d.status]">{{ d.status }}</Badge></td>
                <td class="tnum px-3 py-2.5 text-(--text-secondary)">{{ d.attempts }}</td>
                <td class="px-3 py-2.5 text-(--text-muted)" :title="d.updated_at">{{ formatRelative(d.updated_at) }}</td>
                <td class="px-3 py-2.5 text-right">
                  <Button
                    v-if="d.status === 'dead' || d.status === 'delivered'"
                    size="sm"
                    variant="ghost"
                    :disabled="redeliver.isPending.value"
                    @click="redeliver.mutate(d.id)"
                  >
                    <RefreshCw :size="13" /> Redeliver
                  </Button>
                </td>
              </tr>
            </tbody>
          </table>
          <EmptyState
            v-if="!deliveries.isPending.value && !deliveries.data.value?.items.length"
            title="No deliveries match"
            class="m-4"
          />
        </div>
      </div>
      <EmptyState v-else title="Select a subscription to see its delivery log" />
    </template>
  </section>

  <!-- Events feed -->
  <section v-else class="mt-4">
    <ErrorState v-if="events.isError.value" :error="events.error.value" @retry="events.refetch()" />
    <div v-else class="overflow-hidden rounded-lg border border-(--border) bg-(--surface)">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-(--border) bg-(--canvas) text-left text-[13px] text-(--text-muted)">
            <th class="px-3 py-2 font-medium">Seq</th>
            <th class="px-3 py-2 font-medium">Type</th>
            <th class="px-3 py-2 font-medium">Aggregate</th>
            <th class="px-3 py-2 font-medium">When</th>
          </tr>
        </thead>
        <tbody>
          <SkeletonRows v-if="events.isPending.value" :rows="6" :cols="4" />
          <tr v-for="e in events.data.value?.items" v-else :key="e.seq" class="border-b border-(--border) last:border-0">
            <td class="tnum px-3 py-2.5 text-(--text-muted)">{{ e.seq }}</td>
            <td class="px-3 py-2.5 text-(--text-secondary)">{{ e.envelope.type }}</td>
            <td class="mono px-3 py-2.5 text-[12.5px] text-(--text-muted)">
              {{ e.envelope.aggregate_type }}/{{ e.envelope.aggregate_id }}
            </td>
            <td class="px-3 py-2.5 text-(--text-muted)" :title="e.envelope.occurred_at">
              {{ formatRelative(e.envelope.occurred_at) }}
            </td>
          </tr>
        </tbody>
      </table>
      <EmptyState
        v-if="!events.isPending.value && !events.data.value?.items.length"
        title="No events yet"
        body="Events appear here as your systems write changes; this view refreshes live."
        class="m-4"
      />
    </div>
  </section>

  <WebhookSubscriptionDrawer :open="drawer" :subscription="editing" @close="drawer = false" />

  <Modal
    v-if="confirmDelete"
    :open="!!confirmDelete"
    title="Delete subscription?"
    :message="`Delete '${confirmDelete.name}' and its delivery history? This cannot be undone.`"
    confirm-label="Delete"
    danger
    @close="confirmDelete = undefined"
    @confirm="remove.mutate(confirmDelete)"
  />
</template>
