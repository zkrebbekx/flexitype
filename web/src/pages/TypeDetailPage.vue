<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { api, friendlyError } from '@/lib/api'
import type { AttributeDefinition, Dependency } from '@/lib/api'
import { ancestorsOf } from '@/lib/tree'
import { formatRelative } from '@/lib/format'
import { renderTyped } from '@/lib/values'
import { useToasts } from '@/composables/useToasts'
import PageHeader from '@/components/ui/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import Badge from '@/components/ui/Badge.vue'
import TypeChip from '@/components/ui/TypeChip.vue'
import Tabs from '@/components/ui/Tabs.vue'
import Modal from '@/components/ui/Modal.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import ErrorState from '@/components/ui/ErrorState.vue'
import SkeletonRows from '@/components/ui/SkeletonRows.vue'
import AttributeDrawer from '@/components/AttributeDrawer.vue'
import DependencyDrawer from '@/components/DependencyDrawer.vue'
import RelationshipDefinitionDrawer from '@/components/RelationshipDefinitionDrawer.vue'
import { Plus, Archive, ArchiveRestore, ArrowRight, Pencil } from 'lucide-vue-next'

const route = useRoute()
const typeId = computed(() => String(route.params.id))
const toasts = useToasts()
const queryClient = useQueryClient()

const type = useQuery({
  queryKey: ['type', typeId],
  queryFn: () => api.getType(typeId.value),
})

const attributes = useQuery({
  queryKey: ['attributes', typeId],
  queryFn: () => api.listAttributes({ type_definition_id: typeId.value, include_archived: true, limit: 200 }),
})

const dependencies = useQuery({
  queryKey: ['dependencies', typeId],
  queryFn: () => api.listDependencies({ limit: 200 }),
  select: (page) => {
    const ids = new Set((effective.data.value?.items ?? []).map((e) => e.attribute.id))
    return page.items.filter((d) => ids.has(d.source_attribute_id) || ids.has(d.target_attribute_id))
  },
})

const entities = useQuery({
  queryKey: ['entities', typeId],
  queryFn: () => api.listEntities(typeId.value, { limit: 25 }),
})

const allTypes = useQuery({ queryKey: ['types-all'], queryFn: () => api.listTypes({ limit: 200 }) })

const effective = useQuery({
  queryKey: ['effective-attributes', typeId],
  queryFn: () => api.effectiveAttributes(typeId.value),
})

const children = useQuery({
  queryKey: ['type-children', typeId],
  queryFn: () => api.typeChildren(typeId.value),
})

// Inherited = effective minus own declarations.
const inherited = computed(() =>
  (effective.data.value?.items ?? []).filter((e) => e.declared_in.id !== typeId.value),
)

const ancestorChips = computed(() => {
  const all = allTypes.data.value?.items ?? []
  return ancestorsOf(all, typeId.value)
})

const relDefs = useQuery({
  queryKey: ['relationship-definitions', typeId],
  queryFn: () => api.listRelationshipDefinitions({ type_definition_id: typeId.value, limit: 200 }),
})

const tab = ref('attributes')
const tabs = computed(() => [
  { key: 'attributes', label: 'Attributes', count: effective.data.value?.items.length },
  { key: 'dependencies', label: 'Dependencies', count: dependencies.data.value?.length },
  { key: 'relationships', label: 'Relationships', count: relDefs.data.value?.items.length },
  { key: 'entities', label: 'Entities', count: entities.data.value?.page_info.total_count },
])

const relDrawer = ref(false)

function typeName(id: string): string {
  return allTypes.data.value?.items.find((t) => t.id === id)?.display_name ?? id
}

// Attribute drawer state
const attrDrawer = ref(false)
const editingAttr = ref<AttributeDefinition>()
function openAttr(a?: AttributeDefinition) {
  editingAttr.value = a
  attrDrawer.value = true
}

// Dependency drawer state
const depDrawer = ref(false)
const editingDep = ref<Dependency>()
function openDep(d?: Dependency) {
  editingDep.value = d
  depDrawer.value = true
}

// Archive/restore flows
const confirmArchive = ref<AttributeDefinition>()

const archiveAttr = useMutation({
  mutationFn: (a: AttributeDefinition) => (a.archived_at ? api.restoreAttribute(a.id) : api.archiveAttribute(a.id)),
  onSuccess: (a) => {
    queryClient.invalidateQueries({ queryKey: ['attributes', typeId] })
    toasts.success(a.archived_at ? `"${a.display_name}" archived` : `"${a.display_name}" restored`)
    confirmArchive.value = undefined
  },
  onError: (e) => toasts.error(friendlyError(e)),
})

const archiveDep = useMutation({
  mutationFn: (id: string) => api.archiveDependency(id),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['dependencies'] })
    toasts.success('Dependency archived')
  },
  onError: (e) => toasts.error(friendlyError(e)),
})

function attrName(id: string): string {
  return (effective.data.value?.items ?? []).find((e) => e.attribute.id === id)?.attribute.display_name ?? id
}

function describeDependency(d: Dependency): string {
  const conds = d.conditions
    .map((c) => {
      switch (c.kind) {
        case 'equals':
          return `= ${c.value ? renderTyped(c.value) : '?'}`
        case 'in':
          return `in [${(c.values ?? []).map(renderTyped).join(', ')}]`
        case 'range':
          return `between ${c.min ? renderTyped(c.min) : '−∞'} and ${c.max ? renderTyped(c.max) : '∞'}`
        case 'pattern':
          return `matches ${c.pattern}`
        case 'dynamic':
          return `${c.op?.replaceAll('_', ' ')} ${c.dynamic?.kind === 'relative_time' ? `now ${Number(c.dynamic.amount) >= 0 ? '+' : ''}${c.dynamic.amount} ${c.dynamic.period}` : c.dynamic?.kind}`
      }
    })
    .join(' and ')
  return conds
}

function describeEffect(d: Dependency): string {
  const parts: string[] = []
  if (d.effect.allowed_values?.length) parts.push(`allow only [${d.effect.allowed_values.map(renderTyped).join(', ')}]`)
  if (d.effect.required !== undefined) parts.push(d.effect.required ? 'force required' : 'force optional')
  if (d.effect.constraints?.length) parts.push(`${d.effect.constraints.length} extra constraints`)
  return parts.join('; ') || '—'
}
</script>

<template>
  <PageHeader
    :title="type.data.value?.display_name ?? '…'"
    :crumbs="[{ label: 'Types', to: '/types' }, { label: type.data.value?.display_name ?? '…' }]"
  >
    <span class="mono">{{ type.data.value?.internal_name }}</span> · v{{ type.data.value?.version }}
    <template #actions>
      <span v-if="ancestorChips.length" class="flex items-center gap-1 text-[13px] text-(--text-muted)">
        extends
        <template v-for="(a, i) in ancestorChips" :key="a.id">
          <RouterLink :to="`/types/${a.id}`"><Badge tone="accent">{{ a.display_name }}</Badge></RouterLink>
          <span v-if="i < ancestorChips.length - 1">→</span>
        </template>
      </span>
      <span v-if="children.data.value?.items.length" class="flex items-center gap-1 text-[13px] text-(--text-muted)">
        subtypes
        <RouterLink v-for="c in children.data.value?.items" :key="c.id" :to="`/types/${c.id}`">
          <Badge>{{ c.display_name }}</Badge>
        </RouterLink>
      </span>
      <Badge v-if="type.data.value?.archived_at" tone="warn">archived</Badge>
    </template>
  </PageHeader>

  <ErrorState v-if="type.isError.value" :error="type.error.value" @retry="type.refetch()" />

  <template v-else>
  <Tabs v-model="tab" :tabs="tabs" />

  <!-- Attributes -->
  <section v-if="tab === 'attributes'" class="mt-4">
    <div class="mb-3 flex justify-end">
      <Button variant="primary" size="sm" @click="openAttr()"><Plus :size="14" /> New attribute</Button>
    </div>

    <div class="overflow-hidden rounded-lg border border-(--border) bg-(--surface)">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-(--border) bg-(--canvas) text-left text-[13px] text-(--text-muted)">
            <th class="px-3 py-2 font-medium">Attribute</th>
            <th class="px-3 py-2 font-medium">Type</th>
            <th class="px-3 py-2 font-medium">Rules</th>
            <th class="px-3 py-2 font-medium">Version</th>
            <th class="px-3 py-2 font-medium" />
          </tr>
        </thead>
        <tbody>
          <SkeletonRows v-if="attributes.isPending.value" :rows="4" :cols="5" />
          <tr
            v-for="a in attributes.data.value?.items"
            v-else
            :key="a.id"
            class="border-b border-(--border) last:border-0"
            :class="{ 'opacity-55': a.archived_at }"
          >
            <td class="px-3 py-2.5">
              <button class="font-medium text-(--accent) hover:underline" @click="openAttr(a)">
                {{ a.display_name }}
              </button>
              <span class="mono ml-2 text-[12px] text-(--text-muted)">{{ a.internal_name }}</span>
            </td>
            <td class="px-3 py-2.5"><TypeChip :data-type="a.data_type" /></td>
            <td class="px-3 py-2.5">
              <span class="flex flex-wrap gap-1">
                <Badge v-if="a.required" tone="accent">required</Badge>
                <Badge v-if="a.multi_valued">multi</Badge>
                <Badge v-if="a.unique" tone="warn">unique</Badge>
                <Badge v-if="a.constraints.length">{{ a.constraints.length }} constraints</Badge>
              </span>
            </td>
            <td class="tnum px-3 py-2.5 text-(--text-secondary)">v{{ a.version }}</td>
            <td class="px-3 py-2.5 text-right">
              <span class="flex justify-end gap-1">
                <Button size="sm" variant="ghost" :aria-label="`Edit ${a.display_name}`" @click="openAttr(a)">
                  <Pencil :size="14" />
                </Button>
                <Button
                  v-if="a.archived_at"
                  size="sm"
                  variant="ghost"
                  :aria-label="`Restore ${a.display_name}`"
                  @click="archiveAttr.mutate(a)"
                >
                  <ArchiveRestore :size="14" />
                </Button>
                <Button v-else size="sm" variant="ghost" :aria-label="`Archive ${a.display_name}`" @click="confirmArchive = a">
                  <Archive :size="14" />
                </Button>
              </span>
            </td>
          </tr>
        </tbody>
      </table>

      <EmptyState
        v-if="!attributes.isPending.value && !attributes.data.value?.items.length"
        title="No attributes declared on this type"
        body="Attributes are the typed, constrained fields entities of this type can hold. Inherited attributes appear below."
        class="m-4"
      >
        <template #action>
          <Button variant="primary" @click="openAttr()"><Plus :size="15" /> New attribute</Button>
        </template>
      </EmptyState>
    </div>

    <template v-if="inherited.length">
      <h3 class="mt-6 mb-2 text-sm font-semibold text-(--text-secondary)">Inherited</h3>
      <div class="overflow-hidden rounded-lg border border-(--border) bg-(--surface)">
        <table class="w-full text-sm">
          <tbody>
            <tr v-for="e in inherited" :key="e.attribute.id" class="border-b border-(--border) last:border-0">
              <td class="px-3 py-2.5">
                <span class="font-medium">{{ e.attribute.display_name }}</span>
                <span class="mono ml-2 text-[12px] text-(--text-muted)">{{ e.attribute.internal_name }}</span>
              </td>
              <td class="px-3 py-2.5"><TypeChip :data-type="e.attribute.data_type" /></td>
              <td class="px-3 py-2.5">
                <span class="flex flex-wrap gap-1">
                  <Badge v-if="e.attribute.required" tone="accent">required</Badge>
                  <Badge v-if="e.attribute.unique" tone="warn">unique</Badge>
                </span>
              </td>
              <td class="px-3 py-2.5 text-right text-[13px] text-(--text-muted)">
                from
                <RouterLink :to="`/types/${e.declared_in.id}`" class="text-(--accent) hover:underline">
                  {{ e.declared_in.display_name }}
                </RouterLink>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </section>

  <!-- Dependencies -->
  <section v-else-if="tab === 'dependencies'" class="mt-4">
    <div class="mb-3 flex justify-end">
      <Button variant="primary" size="sm" @click="openDep()"><Plus :size="14" /> New dependency</Button>
    </div>

    <div class="flex flex-col gap-2">
      <article
        v-for="d in dependencies.data.value"
        :key="d.id"
        class="rounded-lg border border-(--border) bg-(--surface) p-3.5"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="text-sm">
            <p class="flex flex-wrap items-center gap-1.5 font-medium">
              {{ attrName(d.source_attribute_id) }}
              <span class="text-(--text-muted)">{{ describeDependency(d) }}</span>
              <ArrowRight :size="14" class="text-(--text-muted)" />
              {{ attrName(d.target_attribute_id) }}
            </p>
            <p class="mt-1 text-[13px] text-(--text-secondary)">{{ describeEffect(d) }}</p>
            <p v-if="d.description" class="mt-1 text-[12.5px] text-(--text-muted)">{{ d.description }}</p>
          </div>
          <div class="flex shrink-0 gap-1">
            <Button size="sm" variant="ghost" aria-label="Edit dependency" @click="openDep(d)"><Pencil :size="14" /></Button>
            <Button size="sm" variant="ghost" aria-label="Archive dependency" @click="archiveDep.mutate(d.id)">
              <Archive :size="14" />
            </Button>
          </div>
        </div>
      </article>

      <EmptyState
        v-if="!dependencies.isPending.value && !dependencies.data.value?.length"
        title="No dependencies"
        body="Dependencies make one attribute's rules react to another's value — cascading picklists, conditional requirements."
      >
        <template #action>
          <Button variant="primary" @click="openDep()"><Plus :size="15" /> New dependency</Button>
        </template>
      </EmptyState>
    </div>
  </section>

  <!-- Relationship types -->
  <section v-else-if="tab === 'relationships'" class="mt-4">
    <div class="mb-3 flex justify-end">
      <Button variant="primary" size="sm" @click="relDrawer = true"><Plus :size="14" /> New relationship type</Button>
    </div>

    <div class="flex flex-col gap-2">
      <article
        v-for="d in relDefs.data.value?.items"
        :key="d.id"
        class="rounded-lg border border-(--border) bg-(--surface) p-3.5"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="text-sm">
            <p class="flex flex-wrap items-center gap-1.5 font-medium">
              {{ d.display_name }}
              <span class="mono text-[12px] text-(--text-muted)">{{ d.internal_name }}</span>
            </p>
            <p class="mt-1 flex items-center gap-1.5 text-[13px] text-(--text-secondary)">
              {{ typeName(d.parent_type_id) }}
              <ArrowRight :size="13" class="text-(--text-muted)" />
              {{ typeName(d.child_type_id) }}
            </p>
            <p class="mt-1 flex gap-1.5 text-[12px] text-(--text-muted)">
              <Badge :tone="d.parent_version_policy === 'pinned' ? 'warn' : 'neutral'">parent: {{ d.parent_version_policy }}</Badge>
              <Badge :tone="d.child_version_policy === 'pinned' ? 'warn' : 'neutral'">child: {{ d.child_version_policy }}</Badge>
              <Badge v-if="d.extends_id" tone="accent">inherits</Badge>
            </p>
          </div>
        </div>
      </article>

      <EmptyState
        v-if="!relDefs.isPending.value && !relDefs.data.value?.items.length"
        title="No relationship types touch this type"
        body="Relationship types link entities of two types — with their own attributes, constraints and version binding."
      >
        <template #action>
          <Button variant="primary" @click="relDrawer = true"><Plus :size="15" /> New relationship type</Button>
        </template>
      </EmptyState>
    </div>
  </section>

  <!-- Entities -->
  <section v-else class="mt-4">
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
          <SkeletonRows v-if="entities.isPending.value" :rows="4" :cols="3" />
          <tr v-for="e in entities.data.value?.items" v-else :key="e.entity_id" class="border-b border-(--border) last:border-0 hover:bg-(--canvas)">
            <td class="px-3 py-2.5">
              <RouterLink
                :to="`/entities/${typeId}/${encodeURIComponent(e.entity_id)}`"
                class="mono font-medium text-(--accent) hover:underline"
              >
                {{ e.entity_id }}
              </RouterLink>
            </td>
            <td class="tnum px-3 py-2.5 text-(--text-secondary)">{{ e.value_count }}</td>
            <td class="px-3 py-2.5 text-(--text-muted)">{{ formatRelative(e.last_updated_at) }}</td>
          </tr>
        </tbody>
      </table>
      <EmptyState
        v-if="!entities.isPending.value && !entities.data.value?.items.length"
        title="No entities hold values yet"
        body="Entities appear here as soon as a value is written against this type."
        class="m-4"
      />
    </div>
  </section>
  </template>

  <AttributeDrawer :open="attrDrawer" :type-id="typeId" :attribute="editingAttr" @close="attrDrawer = false" />
  <RelationshipDefinitionDrawer
    :open="relDrawer"
    :types="allTypes.data.value?.items ?? []"
    :default-parent-id="typeId"
    @close="relDrawer = false"
  />
  <DependencyDrawer
    :open="depDrawer"
    :type-id="typeId"
    :attributes="(effective.data.value?.items ?? []).map((e) => e.attribute)"
    :dependency="editingDep"
    @close="depDrawer = false"
  />

  <Modal
    :open="!!confirmArchive"
    title="Archive attribute?"
    :message="`“${confirmArchive?.display_name}” stops accepting new values. Existing values are kept and the attribute can be restored at any time.`"
    confirm-label="Archive"
    danger
    @close="confirmArchive = undefined"
    @confirm="confirmArchive && archiveAttr.mutate(confirmArchive)"
  />
</template>
