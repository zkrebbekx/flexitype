<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { api } from '@/lib/api'
import type { SuggestSchema } from '@/lib/suggest'
import { formatRelative } from '@/lib/format'
import PageHeader from '@/components/ui/PageHeader.vue'
import Select from '@/components/ui/Select.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import SkeletonRows from '@/components/ui/SkeletonRows.vue'
import Pagination from '@/components/ui/Pagination.vue'
import Badge from '@/components/ui/Badge.vue'
import QueryBar from '@/components/QueryBar.vue'

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

const features = useQuery({
  queryKey: ['features'],
  queryFn: () =>
    fetch('/api/v1/features').then(
      (r) => r.json() as Promise<{ search: boolean; activity: boolean; search_index?: boolean }>,
    ),
  staleTime: Infinity,
})

const queryText = ref('')
const activeQuery = ref('') // the query actually being executed
const selectedType = computed(() => types.data.value?.items.find((t) => t.id === typeId.value))

// Plain listing when no query is running.
const entities = useQuery({
  queryKey: ['entities', typeId, includeSubtypes, cursor],
  queryFn: () =>
    api.listEntities(typeId.value, {
      cursor: cursor.value,
      include_descendants: includeSubtypes.value,
      limit: 25,
    }),
  enabled: computed(() => !!typeId.value && !activeQuery.value),
})

// Query results when a query runs.
const queryResults = useQuery({
  queryKey: ['query', typeId, activeQuery, cursor],
  queryFn: () =>
    api.runQuery({
      type: selectedType.value?.internal_name ?? '',
      q: activeQuery.value,
      cursor: cursor.value,
      limit: 25,
    }),
  enabled: computed(() => !!typeId.value && !!activeQuery.value),
})

const rows = computed(() => (activeQuery.value ? queryResults.data.value : entities.data.value))
const rowsPending = computed(() => (activeQuery.value ? queryResults.isPending.value : entities.isPending.value))

function runQuery(q: string) {
  cursor.value = undefined
  activeQuery.value = q.trim()
}

// Suggestion schema: effective attributes, relationships and their link
// attributes for the selected type.
const effective = useQuery({
  queryKey: ['effective-attributes', typeId],
  queryFn: () => api.effectiveAttributes(typeId.value),
  enabled: computed(() => !!typeId.value),
})
const relDefs = useQuery({
  queryKey: ['relationship-definitions', typeId],
  queryFn: () => api.listRelationshipDefinitions({ type_definition_id: typeId.value, limit: 200 }),
  enabled: computed(() => !!typeId.value),
})
const linkAttrs = useQuery({
  queryKey: ['link-attributes', typeId, relDefs.data],
  queryFn: async () => {
    const out: Record<string, Awaited<ReturnType<typeof api.listAttributes>>['items']> = {}
    for (const def of relDefs.data.value?.items ?? []) {
      const sets = await api.relationshipAttributeSets(def.id)
      const attrs = []
      for (const setId of sets.attribute_set_ids) {
        const page = await api.listAttributes({ type_definition_id: setId, limit: 200 })
        attrs.push(...page.items)
      }
      out[def.internal_name] = attrs
    }
    return out
  },
  enabled: computed(() => !!relDefs.data.value),
})

const suggestSchema = computed<SuggestSchema>(() => ({
  attributes: effective.data.value?.items ?? [],
  relationships: relDefs.data.value?.items ?? [],
  linkAttributes: linkAttrs.data.value ?? {},
  types: types.data.value?.items ?? [],
  searchIndex: features.data.value?.search_index === true,
}))
</script>

<template>
  <PageHeader title="Entities">
    Your domain objects, seen through the values they hold. Pick a type to browse.
  </PageHeader>

  <div class="mb-2 flex max-w-lg items-end gap-4">
    <div class="flex-1">
      <Select v-model="typeId" label="Type" :options="typeOptions" @update:model-value="((cursor = undefined), (activeQuery = ''), (queryText = ''))" />
    </div>
    <label v-if="!activeQuery" class="flex items-center gap-1.5 pb-2 text-[13px] text-(--text-muted)">
      <input v-model="includeSubtypes" type="checkbox" class="accent-(--accent)" @change="cursor = undefined" />
      Include subtypes
    </label>
  </div>

  <div v-if="typeId && features.data.value?.search !== false" class="mb-4">
    <QueryBar
      v-model="queryText"
      :type-internal-name="selectedType?.internal_name ?? ''"
      :schema="suggestSchema"
      @run="runQuery"
    />
    <p v-if="activeQuery" class="mt-1 text-[12px] text-(--text-muted)">
      Showing matches for the query above (latest live values only; archived entities and attributes excluded).
      <button class="text-(--accent) hover:underline" @click="((activeQuery = ''), (queryText = ''), (cursor = undefined))">
        Clear
      </button>
    </p>
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
          <SkeletonRows v-if="rowsPending" :rows="5" :cols="3" />
          <tr
            v-for="e in rows?.items"
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
        v-if="!rowsPending && !rows?.items.length"
        :title="activeQuery ? 'No entities match this query' : 'No entities for this type'"
        :body="activeQuery ? 'Adjust the conditions or clear the query.' : 'Entities appear as soon as your systems write values against this type.'"
        class="m-4"
      />
    </div>

    <Pagination
      :page-info="rows?.page_info"
      :loading="activeQuery ? queryResults.isFetching.value : entities.isFetching.value"
      @next="(c) => (cursor = c)"
      @reset="cursor = undefined"
    />
  </template>

  <EmptyState v-else title="Pick a type to browse its entities" />
</template>
