<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { api } from '@/lib/api'
import type { ActivityEntry } from '@/lib/api'
import PageHeader from '@/components/ui/PageHeader.vue'
import RelativeTime from '@/components/ui/RelativeTime.vue'
import { usePagedCursor } from '@/composables/usePagedCursor'
import Badge from '@/components/ui/Badge.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import DiffView from '@/components/ui/DiffView.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import ErrorState from '@/components/ui/ErrorState.vue'
import SkeletonRows from '@/components/ui/SkeletonRows.vue'
import Pagination from '@/components/ui/Pagination.vue'
import { ChevronDown, ChevronRight } from 'lucide-vue-next'

// Seed filters from the URL so detail pages can deep-link into a
// pre-filtered audit view (e.g. /activity?entity=type_definition&entity_id=…).
const route = useRoute()
const q = (k: string) => (typeof route.query[k] === 'string' ? (route.query[k] as string) : '')
const filters = reactive({ entity: q('entity'), entity_id: q('entity_id'), actor: q('actor') })
const { cursor, canPrevious, next: pageNext, previous: pagePrev, reset: pageReset } = usePagedCursor()

const activity = useQuery({
  queryKey: ['activity', filters, cursor],
  queryFn: () =>
    api.listActivity({
      entity: filters.entity || undefined,
      entity_id: filters.entity_id || undefined,
      actor: filters.actor || undefined,
      cursor: cursor.value,
      limit: 30,
    }),
})

const expanded = ref(new Set<string>())
function toggle(id: string) {
  const next = new Set(expanded.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expanded.value = next
}

const actionTone: Record<ActivityEntry['action'], 'ok' | 'accent' | 'warn' | 'danger'> = {
  created: 'ok',
  updated: 'accent',
  archived: 'warn',
  restored: 'accent',
  removed: 'danger',
}

const ENTITY_KINDS = [
  { value: '', label: 'All entities' },
  { value: 'type_definition', label: 'Type definitions' },
  { value: 'attribute_definition', label: 'Attribute definitions' },
  { value: 'attribute_value', label: 'Attribute values' },
  { value: 'attribute_value_dependency', label: 'Dependencies' },
]
</script>

<template>
  <PageHeader title="Activity">
    Every change, with before/after descriptors, written in the same transaction as the change itself.
  </PageHeader>

  <div class="mb-4 grid max-w-3xl grid-cols-3 gap-3">
    <Select v-model="filters.entity" label="Kind" :options="ENTITY_KINDS" @update:model-value="pageReset" />
    <Input v-model="filters.entity_id" label="Entity ID" mono placeholder="01J… or order-1234" @change="pageReset" />
    <Input v-model="filters.actor" label="Actor" placeholder="service_account:ci" @change="pageReset" />
  </div>

  <div class="overflow-hidden rounded-lg border border-(--border) bg-(--surface)">
    <table class="w-full text-sm">
      <tbody>
        <SkeletonRows v-if="activity.isPending.value" :rows="8" :cols="4" />
        <template v-for="e in activity.data.value?.items" v-else :key="e.id">
          <tr class="cursor-pointer border-b border-(--border) hover:bg-(--canvas)" @click="toggle(e.id)">
            <td class="w-6 py-2.5 pl-3 text-(--text-muted)">
              <ChevronDown v-if="expanded.has(e.id)" :size="15" />
              <ChevronRight v-else :size="15" />
            </td>
            <td class="px-2 py-2.5">
              <Badge :tone="actionTone[e.action]">{{ e.action }}</Badge>
              <span class="ml-2 text-(--text-secondary)">{{ e.entity.replaceAll('_', ' ') }}</span>
              <span class="mono ml-2 text-[12px] text-(--text-muted)">{{ e.entity_id }}</span>
            </td>
            <td class="px-3 py-2.5 text-[13px] text-(--text-secondary)">{{ e.actor }}</td>
            <td class="px-3 py-2.5 text-right text-[13px] whitespace-nowrap text-(--text-muted)">
              <RelativeTime :iso="e.occurred_at" />
            </td>
          </tr>
          <tr v-if="expanded.has(e.id)" class="border-b border-(--border)">
            <td />
            <td colspan="3" class="px-2 pb-3">
              <DiffView :before="e.before" :after="e.after" />
            </td>
          </tr>
        </template>
      </tbody>
    </table>
    <ErrorState v-if="activity.isError.value" :error="activity.error.value" class="m-4" @retry="activity.refetch()" />
    <EmptyState
      v-else-if="!activity.isPending.value && !activity.data.value?.items.length"
      title="No activity matches these filters"
      class="m-4"
    />
  </div>

  <Pagination
    :page-info="activity.data.value?.page_info"
    :loading="activity.isFetching.value"
    :can-previous="canPrevious"
    @next="pageNext"
    @previous="pagePrev"
    @reset="pageReset"
  />
</template>
