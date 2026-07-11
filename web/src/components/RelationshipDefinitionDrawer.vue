<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { api, friendlyError } from '@/lib/api'
import type { RelationshipKind, TypeDefinition, VersionPolicy } from '@/lib/api'
import { useToasts } from '@/composables/useToasts'
import Drawer from '@/components/ui/Drawer.vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import { ArrowLeftRight, ArrowRight } from 'lucide-vue-next'

const props = defineProps<{ open: boolean; types: TypeDefinition[]; defaultParentId?: string }>()
const emit = defineEmits<{ close: [] }>()

const toasts = useToasts()
const queryClient = useQueryClient()

const form = reactive({
  internal_name: '',
  display_name: '',
  description: '',
  kind: 'directed' as RelationshipKind,
  parent_type_id: '',
  child_type_id: '',
  parent_label: '',
  child_label: '',
  extends_id: '',
  parent_version_policy: 'latest' as VersionPolicy,
  child_version_policy: 'latest' as VersionPolicy,
})
const error = ref('')

const isSymmetric = computed(() => form.kind === 'symmetric')

watch(
  () => props.open,
  (open) => {
    if (!open) return
    error.value = ''
    form.internal_name = ''
    form.display_name = ''
    form.description = ''
    form.kind = 'directed'
    form.parent_type_id = props.defaultParentId ?? ''
    form.child_type_id = ''
    form.parent_label = ''
    form.child_label = ''
    form.extends_id = ''
    form.parent_version_policy = 'latest'
    form.child_version_policy = 'latest'
  },
)

// Symmetric pairs never pin (pinning is directional) and have no roles.
watch(isSymmetric, (symmetric) => {
  if (symmetric) {
    form.parent_version_policy = 'latest'
    form.child_version_policy = 'latest'
    form.parent_label = ''
    form.child_label = ''
  }
})

const typeOptions = computed(() => [
  { value: '', label: 'Select…' },
  ...props.types.filter((t) => !t.archived_at).map((t) => ({ value: t.id, label: t.display_name })),
])

// Bases must connect the same endpoints; offer only compatible definitions.
const bases = useQuery({
  queryKey: ['relationship-definitions', 'all'],
  queryFn: () => api.listRelationshipDefinitions({ limit: 200 }),
})
const baseOptions = computed(() => [
  { value: '', label: 'No inheritance' },
  ...(bases.data.value?.items ?? [])
    .filter(
      (d) =>
        d.kind === form.kind &&
        d.parent_type_id === form.parent_type_id &&
        d.child_type_id === form.child_type_id,
    )
    .map((d) => ({ value: d.id, label: d.display_name })),
])

const POLICIES = [
  { value: 'latest', label: 'Track latest version' },
  { value: 'pinned', label: 'Pin a specific version per link' },
]

const KINDS = [
  { value: 'directed', label: 'Directed (parent → child)' },
  { value: 'symmetric', label: 'Symmetric (unordered peers)' },
]

const save = useMutation({
  mutationFn: () =>
    api.createRelationshipDefinition({
      internal_name: form.internal_name,
      display_name: form.display_name,
      description: form.description || undefined,
      kind: form.kind,
      parent_type_id: form.parent_type_id,
      child_type_id: form.child_type_id,
      parent_label: form.parent_label || undefined,
      child_label: form.child_label || undefined,
      extends_id: form.extends_id || undefined,
      parent_version_policy: form.parent_version_policy,
      child_version_policy: form.child_version_policy,
    }),
  onSuccess: (d) => {
    queryClient.invalidateQueries({ queryKey: ['relationship-definitions'] })
    toasts.success(`Relationship "${d.display_name}" created`)
    emit('close')
  },
  onError: (e) => (error.value = friendlyError(e)),
})
</script>

<template>
  <Drawer
    :open="open"
    title="New relationship type"
    subtitle="A named link between two types. It carries its own attributes, editable per link."
    @close="emit('close')"
  >
    <form class="flex flex-col gap-4" @submit.prevent="save.mutate()">
      <div class="grid grid-cols-2 gap-3">
        <Input v-model="form.internal_name" label="Internal name" mono placeholder="uses" hint="snake_case; immutable" />
        <Input v-model="form.display_name" label="Display name" placeholder="Uses" />
      </div>
      <Input v-model="form.description" label="Description" placeholder="Optional" />

      <Select
        v-model="form.kind"
        label="Kind"
        :options="KINDS"
        hint="Directed edges have roles and can pin versions; symmetric links are unordered peers (e.g. compatible_with)."
      />

      <div class="grid grid-cols-[1fr_auto_1fr] items-end gap-2">
        <Select v-model="form.parent_type_id" :label="isSymmetric ? 'Endpoint type A' : 'Parent type'" :options="typeOptions" />
        <ArrowLeftRight v-if="isSymmetric" :size="16" class="mb-2.5 text-(--text-muted)" />
        <ArrowRight v-else :size="16" class="mb-2.5 text-(--text-muted)" />
        <Select v-model="form.child_type_id" :label="isSymmetric ? 'Endpoint type B' : 'Child type'" :options="typeOptions" />
      </div>

      <div v-if="!isSymmetric" class="grid grid-cols-2 gap-3">
        <Input v-model="form.parent_label" label="Parent role label" placeholder="e.g. assembly" hint="Display only; optional" />
        <Input v-model="form.child_label" label="Child role label" placeholder="e.g. component" hint="Display only; optional" />
      </div>

      <div v-if="!isSymmetric" class="grid grid-cols-2 gap-3">
        <Select v-model="form.parent_version_policy" label="Parent version binding" :options="POLICIES" />
        <Select v-model="form.child_version_policy" label="Child version binding" :options="POLICIES" />
      </div>

      <Select
        v-model="form.extends_id"
        label="Inherits from"
        :options="baseOptions"
        hint="An extending relationship layers its attributes on top of the base's; endpoints must match."
      />

      <p v-if="error" class="rounded-md bg-(--danger-soft) px-3 py-2 text-[13px] text-(--danger)">{{ error }}</p>
    </form>

    <template #footer>
      <div class="flex justify-end gap-2">
        <Button @click="emit('close')">Cancel</Button>
        <Button variant="primary" :disabled="save.isPending.value" @click="save.mutate()">Create relationship type</Button>
      </div>
    </template>
  </Drawer>
</template>
