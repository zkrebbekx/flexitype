<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { api, friendlyError } from '@/lib/api'
import type { AttributeDefinition, AttributeValue, EffectiveSchema, EntityLink, RelationshipDefinition } from '@/lib/api'
import { formatRelative, renderValue } from '@/lib/format'
import { fromApiValue, toApiValue } from '@/lib/values'
import { useToasts } from '@/composables/useToasts'
import PageHeader from '@/components/ui/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import Badge from '@/components/ui/Badge.vue'
import TypeChip from '@/components/ui/TypeChip.vue'
import Modal from '@/components/ui/Modal.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import ErrorState from '@/components/ui/ErrorState.vue'
import ValueInput from '@/components/ValueInput.vue'
import Select from '@/components/ui/Select.vue'
import Input from '@/components/ui/Input.vue'
import { ArrowLeftRight, ArrowRight, Link2, Pencil, Plus, Trash2, Unlink } from 'lucide-vue-next'

const route = useRoute()
const router = useRouter()
const typeId = computed(() => String(route.params.typeId))
const entityId = computed(() => String(route.params.entityId))
const toasts = useToasts()
const queryClient = useQueryClient()

const type = useQuery({ queryKey: ['type', typeId], queryFn: () => api.getType(typeId.value) })
const effective = useQuery({
  queryKey: ['effective-attributes', typeId],
  queryFn: () => api.effectiveAttributes(typeId.value),
})
const values = useQuery({
  queryKey: ['entity-values', typeId, entityId],
  queryFn: () => api.listEntityValues(typeId.value, entityId.value),
})

interface Row {
  attribute: AttributeDefinition
  declaredIn: { id: string; display_name: string }
  values: AttributeValue[]
}

// Every attribute of the entity's inherited schema renders a row —
// including ones with no value yet, so "what's missing" is visible at a
// glance. Inherited rows carry their declaring type.
const rows = computed<Row[]>(() => {
  const attrs = effective.data.value?.items ?? []
  const vals = values.data.value?.items ?? []
  return attrs
    .filter((e) => !e.attribute.archived_at)
    .map((e) => ({
      attribute: e.attribute,
      declaredIn: e.declared_in,
      values: vals.filter((v) => v.attribute_definition_id === e.attribute.id),
    }))
})

// --- editing -----------------------------------------------------------------

const editor = reactive({
  open: false,
  attribute: undefined as AttributeDefinition | undefined,
  input: '',
  error: '',
  schema: undefined as EffectiveSchema | undefined,
})

async function openEditor(attribute: AttributeDefinition, current?: AttributeValue) {
  editor.attribute = attribute
  editor.input = current ? fromApiValue(attribute.data_type, current.value) : ''
  editor.error = ''
  editor.schema = undefined
  editor.open = true
  try {
    editor.schema = await api.effectiveSchema(typeId.value, entityId.value, attribute.id)
  } catch {
    // Effective schema is an enhancement; the editor still works without it.
  }
}

// Allowed values for the editor: dependency narrowing wins, else the enum's
// own members.
const editorAllowed = computed<string[] | undefined>(() => {
  if (editor.schema?.restricted) return (editor.schema.allowed_values ?? []).map(String)
  const oneOf = editor.attribute?.constraints.find((c) => c.kind === 'one_of')
  if (oneOf?.values) return oneOf.values.map((v) => String(v.value))
  return undefined
})

const blockedByDependency = computed(() => editor.schema?.restricted && editorAllowed.value?.length === 0)

const setValue = useMutation({
  mutationFn: () => {
    if (!editor.attribute) throw new Error('no attribute selected')
    return api.setValue({
      attribute_definition_id: editor.attribute.id,
      entity_id: entityId.value,
      // The entity's declared type: inherited attributes anchor here.
      type_definition_id: typeId.value,
      value: toApiValue(editor.attribute.data_type, editor.input),
    })
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['entity-values', typeId, entityId] })
    toasts.success('Value saved')
    editor.open = false
  },
  onError: (e) => (editor.error = friendlyError(e)),
})

// --- entity lifecycle ----------------------------------------------------------

const confirmDelete = ref(false)
const deleteEntity = useMutation({
  mutationFn: () => api.deleteEntity(typeId.value, entityId.value),
  onSuccess: (res) => {
    toasts.success(
      `Entity deleted (${res.values_removed} value${res.values_removed === 1 ? '' : 's'}, ` +
        `${res.relationships_gone} relationship${res.relationships_gone === 1 ? '' : 's'})`,
    )
    confirmDelete.value = false
    router.push('/entities')
  },
  onError: (e) => {
    confirmDelete.value = false
    toasts.error(friendlyError(e))
  },
})

// --- relationships -------------------------------------------------------------

const links = useQuery({
  queryKey: ['entity-relationships', typeId, entityId],
  queryFn: () => api.listEntityRelationships(typeId.value, entityId.value),
})

const relDefs = useQuery({
  queryKey: ['relationship-definitions', typeId],
  queryFn: () => api.listRelationshipDefinitions({ type_definition_id: typeId.value, limit: 200 }),
})

const linker = reactive({
  open: false,
  definitionId: '',
  counterpart: '',
  parentVersion: '',
  childVersion: '',
  error: '',
})

const linkerDef = computed<RelationshipDefinition | undefined>(() =>
  relDefs.data.value?.items.find((d) => d.id === linker.definitionId),
)
// Which side is this entity on for the selected definition?
const linkerRole = computed(() => (linkerDef.value?.parent_type_id === typeId.value ? 'parent' : 'child'))

const createLink = useMutation({
  mutationFn: () => {
    const def = linkerDef.value
    if (!def) throw new Error('pick a relationship type')
    const isParent = linkerRole.value === 'parent'
    return api.link({
      relationship_definition_id: def.id,
      parent_entity_id: isParent ? entityId.value : linker.counterpart,
      child_entity_id: isParent ? linker.counterpart : entityId.value,
      parent_type_version: linker.parentVersion ? Number(linker.parentVersion) : undefined,
      child_type_version: linker.childVersion ? Number(linker.childVersion) : undefined,
    })
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['entity-relationships', typeId, entityId] })
    toasts.success('Entities linked')
    linker.open = false
  },
  onError: (e) => (linker.error = friendlyError(e)),
})

const confirmUnlink = ref<EntityLink>()
const unlink = useMutation({
  mutationFn: (l: EntityLink) => api.unlink(l.relationship.id),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['entity-relationships', typeId, entityId] })
    toasts.success('Unlinked')
    confirmUnlink.value = undefined
  },
  onError: (e) => toasts.error(friendlyError(e)),
})

function counterpartOf(l: EntityLink): string {
  return l.role === 'parent' ? l.relationship.child_entity_id : l.relationship.parent_entity_id
}

// Role chip text: symmetric links have no roles; directed ones prefer the
// definition's display labels over parent/child.
function roleLabel(l: EntityLink): string {
  if (l.definition.kind === 'symmetric') return 'linked'
  if (l.role === 'parent') return l.definition.parent_label || 'parent'
  return l.definition.child_label || 'child'
}

const confirmRemove = ref<AttributeValue>()
const removeValue = useMutation({
  mutationFn: (v: AttributeValue) => api.removeValue(v.id),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['entity-values', typeId, entityId] })
    toasts.success('Value removed')
    confirmRemove.value = undefined
  },
  onError: (e) => toasts.error(friendlyError(e)),
})
</script>

<template>
  <PageHeader
    :title="entityId"
    :crumbs="[
      { label: 'Entities', to: '/entities' },
      { label: type.data.value?.display_name ?? '…', to: `/types/${typeId}` },
      { label: entityId },
    ]"
  >
    <template #actions>
      <Button variant="danger" @click="confirmDelete = true">
        <Trash2 :size="14" /> Delete entity
      </Button>
    </template>
    Every attribute this {{ type.data.value?.display_name?.toLowerCase() ?? 'type' }} can hold, with its current values.
  </PageHeader>

  <ErrorState
    v-if="effective.isError.value || values.isError.value || type.isError.value"
    :error="effective.error.value ?? values.error.value ?? type.error.value"
    class="mb-2"
    @retry="((effective.refetch(), values.refetch(), type.refetch()))"
  />

  <div v-else class="flex flex-col gap-2">
    <article
      v-for="row in rows"
      :key="row.attribute.id"
      class="rounded-lg border border-(--border) bg-(--surface) px-4 py-3"
    >
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex items-center gap-2.5">
          <TypeChip :data-type="row.attribute.data_type" />
          <span class="text-sm font-medium">{{ row.attribute.display_name }}</span>
          <Badge v-if="row.attribute.required && !row.values.length" tone="danger">required, missing</Badge>
          <Badge v-else-if="row.attribute.required" tone="accent">required</Badge>
          <Badge v-if="row.attribute.multi_valued">multi</Badge>
          <Badge v-if="row.attribute.unique" tone="warn">unique</Badge>
          <span v-if="row.declaredIn.id !== typeId" class="text-[12px] text-(--text-muted)">
            from {{ row.declaredIn.display_name }}
          </span>
        </div>
        <Button
          v-if="!row.values.length || row.attribute.multi_valued"
          size="sm"
          variant="ghost"
          @click="openEditor(row.attribute)"
        >
          <Plus :size="14" /> {{ row.values.length ? 'Add value' : 'Set value' }}
        </Button>
      </div>

      <ul v-if="row.values.length" class="mt-2 flex flex-col gap-1">
        <li
          v-for="v in row.values"
          :key="v.id"
          class="flex items-center justify-between gap-3 rounded-md bg-(--canvas) px-3 py-1.5"
        >
          <span class="mono min-w-0 flex-1 truncate text-[13px]">{{ renderValue(v.value) }}</span>
          <span class="flex shrink-0 items-center gap-2 text-[12px] text-(--text-muted)">
            <span :title="`Validated against definition v${v.definition_version}`">def v{{ v.definition_version }}</span>
            <span v-if="v.definition_version < row.attribute.version" title="The definition has changed since this value was validated">
              <Badge tone="warn">stale</Badge>
            </span>
            <span>{{ formatRelative(v.updated_at) }}</span>
            <Button size="sm" variant="ghost" aria-label="Edit value" @click="openEditor(row.attribute, v)">
              <Pencil :size="13" />
            </Button>
            <Button size="sm" variant="ghost" aria-label="Remove value" @click="confirmRemove = v">
              <Trash2 :size="13" />
            </Button>
          </span>
        </li>
      </ul>
      <p v-else class="mt-1.5 text-[13px] text-(--text-muted)">No value.</p>
    </article>

    <EmptyState
      v-if="!effective.isPending.value && !rows.length"
      title="This type has no attributes"
      body="Define attributes on the type (or an ancestor) first; then entities can hold values."
    />
  </div>

  <!-- Relationships -->
  <section class="mt-6">
    <div class="mb-2 flex items-center justify-between">
      <h2 class="flex items-center gap-1.5 text-base font-semibold"><Link2 :size="16" /> Relationships</h2>
      <Button
        v-if="relDefs.data.value?.items.length"
        size="sm"
        variant="ghost"
        @click="((linker.open = true), (linker.error = ''), (linker.definitionId = relDefs.data.value?.items[0]?.id ?? ''), (linker.counterpart = ''), (linker.parentVersion = ''), (linker.childVersion = ''))"
      >
        <Plus :size="14" /> Link entity
      </Button>
    </div>

    <div class="flex flex-col gap-1.5">
      <div
        v-for="l in links.data.value?.items"
        :key="l.relationship.id"
        class="flex items-center justify-between gap-3 rounded-lg border border-(--border) bg-(--surface) px-4 py-2.5 text-sm"
      >
        <span class="flex min-w-0 flex-wrap items-center gap-1.5">
          <Badge :tone="l.definition.kind === 'symmetric' ? 'neutral' : l.role === 'parent' ? 'accent' : 'neutral'">{{ roleLabel(l) }}</Badge>
          <span class="font-medium">{{ l.definition.display_name }}</span>
          <ArrowLeftRight v-if="l.definition.kind === 'symmetric'" :size="13" class="text-(--text-muted)" />
          <ArrowRight v-else :size="13" class="text-(--text-muted)" />
          <span class="mono truncate">{{ counterpartOf(l) }}</span>
          <Badge v-if="l.relationship.parent_type_version" tone="warn">parent v{{ l.relationship.parent_type_version }}</Badge>
          <Badge v-if="l.relationship.child_type_version" tone="warn">child v{{ l.relationship.child_type_version }}</Badge>
        </span>
        <Button size="sm" variant="ghost" aria-label="Unlink" @click="confirmUnlink = l"><Unlink :size="14" /></Button>
      </div>

      <EmptyState
        v-if="!links.isPending.value && !links.data.value?.items.length"
        title="No relationships"
        body="Links connect this entity to entities of related types, with their own attributes and version binding."
      />
    </div>
  </section>

  <!-- Link editor -->
  <Modal :open="linker.open" role="dialog" title="Link entity" @close="linker.open = false" @confirm="createLink.mutate()">
    <template #actions>
      <div class="w-full">
        <div class="flex flex-col gap-3">
          <Select
            v-model="linker.definitionId"
            label="Relationship type"
            :options="(relDefs.data.value?.items ?? []).map((d) => ({ value: d.id, label: d.display_name }))"
          />
          <Input
            v-model="linker.counterpart"
            :label="linkerRole === 'parent' ? 'Child entity ID' : 'Parent entity ID'"
            mono
            placeholder="order-1234"
          />
          <Input
            v-if="linkerDef?.parent_version_policy === 'pinned'"
            v-model="linker.parentVersion"
            type="number"
            label="Pin parent type version"
          />
          <Input
            v-if="linkerDef?.child_version_policy === 'pinned'"
            v-model="linker.childVersion"
            type="number"
            label="Pin child type version"
          />
          <p v-if="linker.error" class="rounded-md bg-(--danger-soft) px-3 py-2 text-[13px] text-(--danger)">
            {{ linker.error }}
          </p>
        </div>
        <div class="mt-4 flex justify-end gap-2">
          <Button @click="linker.open = false">Cancel</Button>
          <Button variant="primary" :disabled="createLink.isPending.value" @click="createLink.mutate()">Link</Button>
        </div>
      </div>
    </template>
  </Modal>

  <Modal
    :open="!!confirmUnlink"
    title="Unlink?"
    :message="`The link and its attributes are archived; the entities themselves are untouched.`"
    confirm-label="Unlink"
    danger
    @close="confirmUnlink = undefined"
    @confirm="confirmUnlink && unlink.mutate(confirmUnlink)"
  />

  <!-- Value editor -->
  <Modal
    :open="editor.open"
    role="dialog"
    :title="`${editor.attribute?.display_name ?? ''} on ${entityId}`"
    @close="editor.open = false"
    @confirm="setValue.mutate()"
  >
    <template #actions>
      <div class="w-full">
        <div v-if="editor.schema?.restricted" class="mb-3 rounded-md bg-(--accent-soft) px-3 py-2 text-[13px] text-(--accent)">
          <template v-if="blockedByDependency">
            Dependencies currently allow <strong>no value</strong> for this attribute on this entity — adjust the source
            attributes first.
          </template>
          <template v-else>Narrowed by dependencies to {{ editorAllowed?.length }} allowed value(s).</template>
        </div>

        <ValueInput
          v-if="editor.attribute && !blockedByDependency"
          v-model="editor.input"
          :data-type="editor.attribute.data_type"
          :allowed-values="editorAllowed"
          label="Value"
          :error="editor.error"
        />
        <p v-else-if="editor.error" class="text-[13px] text-(--danger)">{{ editor.error }}</p>

        <div class="mt-4 flex justify-end gap-2">
          <Button @click="editor.open = false">Cancel</Button>
          <Button variant="primary" :disabled="setValue.isPending.value || blockedByDependency" @click="setValue.mutate()">
            Save value
          </Button>
        </div>
      </div>
    </template>
  </Modal>

  <Modal
    :open="!!confirmRemove"
    title="Remove value?"
    :message="`Removing is a soft delete: the value “${renderValue(confirmRemove?.value)}” is archived and stays in the audit trail.`"
    confirm-label="Remove"
    danger
    @close="confirmRemove = undefined"
    @confirm="confirmRemove && removeValue.mutate(confirmRemove)"
  />

  <Modal
    :open="confirmDelete"
    title="Delete this entity?"
    :message="`This archives every value of “${entityId}” and unlinks all its relationships in one step. Soft delete: the data stays in the audit trail.`"
    confirm-label="Delete entity"
    danger
    @close="confirmDelete = false"
    @confirm="deleteEntity.mutate()"
  />
</template>
