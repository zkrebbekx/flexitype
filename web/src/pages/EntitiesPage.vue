<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { api, friendlyError } from '@/lib/api'
import type { SuggestSchema } from '@/lib/suggest'
import type { SavedView } from '@/lib/api'
import { useToasts } from '@/composables/useToasts'
import PageHeader from '@/components/ui/PageHeader.vue'
import RelativeTime from '@/components/ui/RelativeTime.vue'
import { usePagedCursor } from '@/composables/usePagedCursor'
import Select from '@/components/ui/Select.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import ErrorState from '@/components/ui/ErrorState.vue'
import SkeletonRows from '@/components/ui/SkeletonRows.vue'
import Pagination from '@/components/ui/Pagination.vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Modal from '@/components/ui/Modal.vue'
import QueryBar from '@/components/QueryBar.vue'
import ImportWizard from '@/components/ImportWizard.vue'
import DuplicatesDrawer from '@/components/DuplicatesDrawer.vue'
import { Bookmark, Copy, Download, Table2, Trash2, Upload } from 'lucide-vue-next'

const types = useQuery({ queryKey: ['types-all'], queryFn: () => api.listTypes({ limit: 200 }) })
const typeId = ref('')
const includeSubtypes = ref(false)
const { cursor, canPrevious, next: pageNext, previous: pagePrev, reset: pageReset } = usePagedCursor()

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
const rowsError = computed(() => (activeQuery.value ? queryResults.error.value : entities.error.value))

function runQuery(q: string) {
  pageReset()
  activeQuery.value = q.trim()
}

// --- saved views ---------------------------------------------------------------
const route = useRoute()
const router = useRouter()
const queryClient = useQueryClient()
const toasts = useToasts()

const savedViews = useQuery({ queryKey: ['saved-views'], queryFn: () => api.listSavedViews() })
const selectedViewId = ref('')
const viewOptions = computed(() => [
  { value: '', label: 'Saved views…' },
  ...(savedViews.data.value?.items ?? []).map((v) => ({ value: v.id, label: v.name })),
])

function applyView(view: SavedView) {
  const t = types.data.value?.items.find((x) => x.internal_name === view.root_type)
  if (!t) {
    toasts.error(`View "${view.name}" points at a missing type`)
    return
  }
  typeId.value = t.id
  queryText.value = view.query
  activeQuery.value = view.query.trim()
  gridColumns.value = view.columns ?? []
  selectedViewId.value = view.id
  pageReset()
  router.replace({ query: { ...route.query, view: view.id } })
}

function onViewPicked(id: string) {
  const view = savedViews.data.value?.items.find((v) => v.id === id)
  if (view) applyView(view)
}

// Restore a view addressed by URL once the types + views have loaded.
watch(
  () => [savedViews.data.value, types.data.value] as const,
  () => {
    const wanted = typeof route.query.view === 'string' ? route.query.view : ''
    if (wanted && wanted !== selectedViewId.value) {
      const view = savedViews.data.value?.items.find((v) => v.id === wanted)
      if (view) applyView(view)
    }
  },
)

const saveModal = reactive({ open: false, name: '', existingId: '' })
function openSave() {
  const current = savedViews.data.value?.items.find((v) => v.id === selectedViewId.value)
  saveModal.existingId = current?.id ?? ''
  saveModal.name = current?.name ?? ''
  saveModal.open = true
}
const saveView = useMutation({
  mutationFn: () => {
    const input = {
      name: saveModal.name.trim(),
      root_type: selectedType.value?.internal_name ?? '',
      query: activeQuery.value || queryText.value,
      columns: gridColumns.value,
    }
    return saveModal.existingId ? api.updateSavedView(saveModal.existingId, input) : api.createSavedView(input)
  },
  onSuccess: (v) => {
    queryClient.invalidateQueries({ queryKey: ['saved-views'] })
    selectedViewId.value = v.id
    saveModal.open = false
    router.replace({ query: { ...route.query, view: v.id } })
    toasts.success(`View "${v.name}" saved`)
  },
  onError: (e) => toasts.error(friendlyError(e)),
})
const deleteView = useMutation({
  mutationFn: (id: string) => api.deleteSavedView(id),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['saved-views'] })
    selectedViewId.value = ''
    router.replace({ query: {} })
    toasts.success('View deleted')
  },
  onError: (e) => toasts.error(friendlyError(e)),
})

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

// --- import / export ---------------------------------------------------------

const importOpen = ref(false)
const duplicatesOpen = ref(false)
const importAttributes = computed(() =>
  (effective.data.value?.items ?? [])
    .filter((e) => !e.attribute.archived_at)
    .map((e) => ({ internal_name: e.attribute.internal_name, display_name: e.attribute.display_name })),
)
// Duplicate rules target attributes owned by this type (not inherited),
// since a rule pins a concrete attribute id on the type.
const ownAttributes = computed(() =>
  (effective.data.value?.items ?? [])
    .filter((e) => !e.attribute.archived_at && e.declared_in.id === typeId.value)
    .map((e) => ({ id: e.attribute.id, internal_name: e.attribute.internal_name, display_name: e.attribute.display_name })),
)
function onImported() {
  queryClient.invalidateQueries({ queryKey: ['entities', typeId] })
  queryClient.invalidateQueries({ queryKey: ['query', typeId] })
}

// --- faceted grid ------------------------------------------------------------

// Chosen value columns (attribute internal names), overlaid onto the row set
// and looked up per entity, so pagination and the row source are unchanged.
const gridColumns = ref<string[]>([])
const columnsOpen = ref(false)
const displayName = (name: string) => importAttributes.value.find((a) => a.internal_name === name)?.display_name ?? name
function toggleColumn(name: string) {
  gridColumns.value = gridColumns.value.includes(name)
    ? gridColumns.value.filter((c) => c !== name)
    : [...gridColumns.value, name]
}

const gridValues = useQuery({
  queryKey: ['grid', typeId, activeQuery, includeSubtypes, cursor, gridColumns],
  queryFn: () =>
    api.gridEntities(typeId.value, {
      attributes: gridColumns.value,
      query: activeQuery.value || undefined,
      include_descendants: includeSubtypes.value,
      cursor: cursor.value || undefined,
    }),
  enabled: computed(() => !!typeId.value && gridColumns.value.length > 0),
})
const gridValueMap = computed(() => {
  const m = new Map<string, Record<string, string>>()
  for (const row of gridValues.data.value?.rows ?? []) m.set(row.entity_id, row.values)
  return m
})
const gridCell = (entityId: string, col: string) => gridValueMap.value.get(entityId)?.[col] ?? ''

// Facets over the current result set for the chosen columns.
const facets = useQuery({
  queryKey: ['facets', typeId, activeQuery, includeSubtypes, gridColumns],
  queryFn: () =>
    api.entityFacets(typeId.value, {
      attributes: gridColumns.value,
      query: activeQuery.value || undefined,
      include_descendants: includeSubtypes.value,
    }),
  enabled: computed(() => !!typeId.value && gridColumns.value.length > 0),
})
// Clicking a facet composes an FQL equality term and runs the query.
function applyFacet(attr: string, value: string) {
  const term = `${attr} = ${JSON.stringify(value)}`
  queryText.value = queryText.value.trim() ? `${queryText.value.trim()} and ${term}` : term
  runQuery(queryText.value)
}

// Export the current view: the active FQL filter narrows the rows; the type's
// effective attributes are the columns. Streams a CSV download.
function exportCurrent() {
  const url = api.exportEntitiesUrl(typeId.value, {
    attributes: importAttributes.value.map((a) => a.internal_name),
    query: activeQuery.value || undefined,
  })
  const a = document.createElement('a')
  a.href = url
  a.download = `${selectedType.value?.internal_name ?? 'export'}.csv`
  document.body.appendChild(a)
  a.click()
  a.remove()
}
</script>

<template>
  <PageHeader title="Entities">
    Your domain objects, seen through the values they hold. Pick a type to browse.
  </PageHeader>

  <div class="mb-2 flex flex-wrap items-end gap-4">
    <div class="w-72">
      <Select v-model="typeId" label="Type" :options="typeOptions" @update:model-value="() => (pageReset(), (activeQuery = ''), (queryText = ''), (selectedViewId = ''), (gridColumns = []))" />
    </div>
    <label v-if="!activeQuery" class="flex items-center gap-1.5 pb-2 text-[13px] text-(--text-muted)">
      <input v-model="includeSubtypes" type="checkbox" class="accent-(--accent)" @change="pageReset" />
      Include subtypes
    </label>

    <div v-if="savedViews.data.value?.items?.length" class="w-56">
      <Select v-model="selectedViewId" label="View" :options="viewOptions" @update:model-value="onViewPicked" />
    </div>
    <div class="flex items-center gap-1.5 pb-1">
      <Button v-if="typeId" size="sm" @click="openSave"><Bookmark :size="14" /> Save view</Button>
      <Button v-if="typeId" size="sm" @click="importOpen = true"><Upload :size="14" /> Import</Button>
      <Button v-if="typeId" size="sm" @click="exportCurrent"><Download :size="14" /> Export</Button>
      <Button v-if="typeId" size="sm" @click="duplicatesOpen = true"><Copy :size="14" /> Duplicates</Button>
      <div v-if="typeId" class="relative">
        <Button size="sm" @click="columnsOpen = !columnsOpen">
          <Table2 :size="14" /> Columns<span v-if="gridColumns.length"> ({{ gridColumns.length }})</span>
        </Button>
        <div
          v-if="columnsOpen"
          class="absolute right-0 z-20 mt-1 max-h-72 w-60 overflow-y-auto rounded-md border border-(--border-strong) bg-(--surface) p-1.5 shadow-lg"
        >
          <p v-if="!importAttributes.length" class="px-2 py-1 text-[13px] text-(--text-muted)">No attributes.</p>
          <label
            v-for="a in importAttributes"
            :key="a.internal_name"
            class="flex cursor-pointer items-center gap-2 rounded px-2 py-1 text-[13px] hover:bg-(--canvas)"
          >
            <input
              type="checkbox"
              class="accent-(--accent)"
              :checked="gridColumns.includes(a.internal_name)"
              @change="toggleColumn(a.internal_name)"
            />
            {{ a.display_name }}
          </label>
        </div>
      </div>
      <Button
        v-if="selectedViewId"
        size="sm"
        variant="ghost"
        aria-label="Delete view"
        @click="deleteView.mutate(selectedViewId)"
      >
        <Trash2 :size="14" />
      </Button>
    </div>
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
      <button class="text-(--accent) hover:underline" @click="() => ((activeQuery = ''), (queryText = ''), pageReset())">
        Clear
      </button>
    </p>
  </div>

  <div
    v-if="typeId && gridColumns.length && Object.keys(facets.data.value?.facets ?? {}).length"
    class="mb-3 flex flex-wrap gap-x-6 gap-y-2 rounded-lg border border-(--border) bg-(--surface) px-4 py-3"
  >
    <div v-for="col in gridColumns" :key="col">
      <template v-if="(facets.data.value?.facets[col] ?? []).length">
        <p class="mb-1 text-[11px] font-medium tracking-wide text-(--text-muted) uppercase">{{ displayName(col) }}</p>
        <div class="flex flex-wrap gap-1">
          <button
            v-for="b in (facets.data.value?.facets[col] ?? []).slice(0, 8)"
            :key="b.value"
            class="rounded-full border border-(--border) px-2 py-0.5 text-[12px] hover:border-(--accent) hover:text-(--accent)"
            @click="applyFacet(col, b.value)"
          >
            {{ b.value }} <span class="text-(--text-muted)">({{ b.count }})</span>
          </button>
        </div>
      </template>
    </div>
  </div>

  <template v-if="typeId">
    <div class="overflow-x-auto rounded-lg border border-(--border) bg-(--surface)">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-(--border) bg-(--canvas) text-left text-[13px] text-(--text-muted)">
            <th class="px-3 py-2 font-medium">Entity</th>
            <th v-for="col in gridColumns" :key="col" class="px-3 py-2 font-medium whitespace-nowrap">{{ displayName(col) }}</th>
            <th class="px-3 py-2 font-medium">Values</th>
            <th class="px-3 py-2 font-medium">Last change</th>
          </tr>
        </thead>
        <tbody>
          <SkeletonRows v-if="rowsPending" :rows="5" :cols="3 + gridColumns.length" />
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
            <td
              v-for="col in gridColumns"
              :key="col"
              class="max-w-52 truncate px-3 py-2.5 text-(--text-secondary)"
              :title="gridCell(e.entity_id, col)"
            >
              {{ gridCell(e.entity_id, col) || '—' }}
            </td>
            <td class="tnum px-3 py-2.5 text-(--text-secondary)">{{ e.value_count }}</td>
            <td class="px-3 py-2.5 text-(--text-muted)"><RelativeTime :iso="e.last_updated_at" /></td>
          </tr>
        </tbody>
      </table>
      <ErrorState v-if="rowsError" :error="rowsError" class="m-4" @retry="activeQuery ? queryResults.refetch() : entities.refetch()" />
      <EmptyState
        v-else-if="!rowsPending && !rows?.items?.length"
        :title="activeQuery ? 'No entities match this query' : 'No entities for this type'"
        :body="activeQuery ? 'Adjust the conditions or clear the query.' : 'Entities appear as soon as your systems write values against this type.'"
        class="m-4"
      />
    </div>

    <Pagination
      :page-info="rows?.page_info"
      :loading="activeQuery ? queryResults.isFetching.value : entities.isFetching.value"
      :can-previous="canPrevious"
      @next="pageNext"
      @previous="pagePrev"
      @reset="pageReset"
    />
  </template>

  <EmptyState v-else title="Pick a type to browse its entities" />

  <Modal
    :open="saveModal.open"
    role="dialog"
    :title="saveModal.existingId ? 'Update view' : 'Save view'"
    @close="saveModal.open = false"
    @confirm="saveView.mutate()"
  >
    <template #actions>
      <div class="w-full">
        <Input v-model="saveModal.name" label="View name" placeholder="Active bikes" />
        <p class="mt-2 text-[13px] text-(--text-muted)">
          Saves the current type and query. Reopen it any time — the view is shareable by its URL.
        </p>
        <div class="mt-4 flex justify-end gap-2">
          <Button @click="saveModal.open = false">Cancel</Button>
          <Button variant="primary" :disabled="!saveModal.name.trim() || saveView.isPending.value" @click="saveView.mutate()">
            Save
          </Button>
        </div>
      </div>
    </template>
  </Modal>

  <ImportWizard
    :open="importOpen"
    :type-id="typeId"
    :type-name="selectedType?.display_name ?? 'entities'"
    :attributes="importAttributes"
    @close="importOpen = false"
    @imported="onImported"
  />

  <DuplicatesDrawer
    :open="duplicatesOpen"
    :type-id="typeId"
    :type-name="selectedType?.display_name ?? 'entities'"
    :attributes="ownAttributes"
    @close="duplicatesOpen = false"
  />
</template>
