<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { api } from '@/lib/api'
import { formatRelative } from '@/lib/format'
import PageHeader from '@/components/ui/PageHeader.vue'
import Select from '@/components/ui/Select.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import SkeletonRows from '@/components/ui/SkeletonRows.vue'
import Pagination from '@/components/ui/Pagination.vue'

const types = useQuery({ queryKey: ['types-all'], queryFn: () => api.listTypes({ limit: 200 }) })
const typeId = ref('')
const includeSubtypes = ref(false)
const cursor = ref<string>()

function typeName(id: string): string {
  return types.data.value?.items.find((t) => t.id === id)?.display_name ?? id
}

const typeOptions = computed(() => [
  { value: '', label: 'Select a type…' },
  ...(types.data.value?.items ?? []).map((t) => ({ value: t.id, label: t.display_name })),
])

const entities = useQuery({
  queryKey: ['entities', typeId, includeSubtypes, cursor],
  queryFn: () =>
    api.listEntities(typeId.value, {
      cursor: cursor.value,
      include_descendants: includeSubtypes.value,
      limit: 25,
    }),
  enabled: computed(() => !!typeId.value),
})
</script>

<template>
  <PageHeader title="Entities">
    Your domain objects, seen through the values they hold. Pick a type to browse.
  </PageHeader>

  <div class="mb-4 flex max-w-lg items-end gap-4">
    <div class="flex-1">
      <Select v-model="typeId" label="Type" :options="typeOptions" @update:model-value="cursor = undefined" />
    </div>
    <label class="flex items-center gap-1.5 pb-2 text-[13px] text-(--text-muted)">
      <input v-model="includeSubtypes" type="checkbox" class="accent-(--accent)" @change="cursor = undefined" />
      Include subtypes
    </label>
  </div>

  <template v-if="typeId">
    <div class="overflow-hidden rounded-lg border border-(--border) bg-(--surface)">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-(--border) bg-(--canvas) text-left text-[13px] text-(--text-muted)">
            <th class="px-3 py-2 font-medium">Entity</th>
            <th class="px-3 py-2 font-medium">Values</th>
            <th class="px-3 py-2 font-medium">Last change</th>
          </tr>
        </thead>
        <tbody>
          <SkeletonRows v-if="entities.isPending.value" :rows="5" :cols="3" />
          <tr
            v-for="e in entities.data.value?.items"
            v-else
            :key="e.entity_id"
            class="border-b border-(--border) last:border-0 hover:bg-(--canvas)"
          >
            <td class="px-3 py-2.5">
              <RouterLink
                :to="`/entities/${e.type_definition_id || typeId}/${encodeURIComponent(e.entity_id)}`"
                class="mono font-medium text-(--accent) hover:underline"
              >
                {{ e.entity_id }}
              </RouterLink>
              <Badge v-if="e.type_definition_id && e.type_definition_id !== typeId" class="ml-2" tone="accent">
                {{ typeName(e.type_definition_id) }}
              </Badge>
            </td>
            <td class="tnum px-3 py-2.5 text-(--text-secondary)">{{ e.value_count }}</td>
            <td class="px-3 py-2.5 text-(--text-muted)">{{ formatRelative(e.last_updated_at) }}</td>
          </tr>
        </tbody>
      </table>
      <EmptyState
        v-if="!entities.isPending.value && !entities.data.value?.items.length"
        title="No entities for this type"
        body="Entities appear as soon as your systems write values against this type."
        class="m-4"
      />
    </div>

    <Pagination
      :page-info="entities.data.value?.page_info"
      :loading="entities.isFetching.value"
      @next="(c) => (cursor = c)"
      @reset="cursor = undefined"
    />
  </template>

  <EmptyState v-else title="Pick a type to browse its entities" />
</template>
