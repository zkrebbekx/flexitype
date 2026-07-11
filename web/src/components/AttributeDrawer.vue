<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { api, ApiError, DATA_TYPES, friendlyError } from '@/lib/api'
import type { AttributeDefinition, Constraint, DataType } from '@/lib/api'
import { renderTyped, toApiValue, typedValue } from '@/lib/values'
import { slug } from '@/lib/validation'
import { useToasts } from '@/composables/useToasts'
import Drawer from '@/components/ui/Drawer.vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import Toggle from '@/components/ui/Toggle.vue'
import Badge from '@/components/ui/Badge.vue'
import ValueInput from '@/components/ValueInput.vue'
import { CircleCheck, CircleX, FlaskConical, Plus, X } from 'lucide-vue-next'

const props = defineProps<{
  open: boolean
  typeId: string
  // Editing when set; creating otherwise.
  attribute?: AttributeDefinition
}>()
const emit = defineEmits<{ close: [] }>()

const toasts = useToasts()
const queryClient = useQueryClient()

const TEXTUAL: DataType[] = ['string', 'enum', 'url', 'email']
const ORDERED: DataType[] = ['integer', 'float', 'decimal', 'date', 'time', 'datetime']

const form = reactive({
  internal_name: '',
  display_name: '',
  description: '',
  data_type: 'string' as DataType,
  required: false,
  multi_valued: false,
  unique: false,
  minLength: '',
  maxLength: '',
  minValue: '',
  maxValue: '',
  pattern: '',
  enumMembers: [] as string[],
  newMember: '',
  group: '',
  sortOrder: '',
  helpText: '',
})
const error = ref('')

// internal_name is immutable on edit; only validate it when creating.
const nameError = computed(() => (props.attribute ? '' : slug(form.internal_name)))
// Surface the name error inline only once the user has typed — emptiness is
// signalled by the disabled submit and the placeholder.
const fieldErrors = computed(() => ({ internal_name: form.internal_name ? nameError.value : '' }))
const canSubmit = computed(() => !!form.display_name.trim() && nameError.value === '')

watch(
  () => [props.open, props.attribute?.id],
  () => {
    if (!props.open) return
    error.value = ''
    tester.result = null
    tester.value = ''
    const a = props.attribute
    form.internal_name = a?.internal_name ?? ''
    form.display_name = a?.display_name ?? ''
    form.description = a?.description ?? ''
    form.data_type = a?.data_type ?? 'string'
    form.required = a?.required ?? false
    form.multi_valued = a?.multi_valued ?? false
    form.unique = a?.unique ?? false
    form.group = a?.group ?? ''
    form.sortOrder = a?.sort_order != null ? String(a.sort_order) : ''
    form.helpText = a?.help_text ?? ''
    form.minLength = ''
    form.maxLength = ''
    form.minValue = ''
    form.maxValue = ''
    form.pattern = ''
    form.enumMembers = []
    for (const c of a?.constraints ?? []) {
      if (c.kind === 'min_length') form.minLength = String(c.n)
      if (c.kind === 'max_length') form.maxLength = String(c.n)
      if (c.kind === 'min_value' && c.value) form.minValue = renderTyped(c.value)
      if (c.kind === 'max_value' && c.value) form.maxValue = renderTyped(c.value)
      if (c.kind === 'pattern') form.pattern = c.expr ?? ''
      if (c.kind === 'one_of') form.enumMembers = (c.values ?? []).map(renderTyped)
    }
  },
  { immediate: true },
)

const isTextual = computed(() => TEXTUAL.includes(form.data_type))
const isOrdered = computed(() => ORDERED.includes(form.data_type))
const isEnum = computed(() => form.data_type === 'enum')

function buildConstraints(): Constraint[] {
  const cs: Constraint[] = []
  if (isTextual.value && form.minLength) cs.push({ kind: 'min_length', n: Number(form.minLength) })
  if (isTextual.value && form.maxLength) cs.push({ kind: 'max_length', n: Number(form.maxLength) })
  if (isOrdered.value && form.minValue)
    cs.push({ kind: 'min_value', value: typedValue(form.data_type, form.minValue) })
  if (isOrdered.value && form.maxValue)
    cs.push({ kind: 'max_value', value: typedValue(form.data_type, form.maxValue) })
  if (isTextual.value && form.pattern) cs.push({ kind: 'pattern', expr: form.pattern })
  if (isEnum.value && form.enumMembers.length)
    cs.push({ kind: 'one_of', values: form.enumMembers.map((m) => typedValue('enum', m)) })
  return cs
}

function addMember() {
  const m = form.newMember.trim()
  if (m && !form.enumMembers.includes(m)) form.enumMembers.push(m)
  form.newMember = ''
}

const save = useMutation({
  mutationFn: async () => {
    const constraints = buildConstraints()
    const presentation = {
      group: form.group || undefined,
      sort_order: form.sortOrder ? Number(form.sortOrder) : undefined,
      help_text: form.helpText || undefined,
    }
    if (props.attribute) {
      return api.updateAttribute(props.attribute.id, {
        display_name: form.display_name,
        description: form.description || undefined,
        required: form.required,
        multi_valued: form.multi_valued,
        unique: form.unique,
        constraints,
        ...presentation,
      })
    }
    return api.createAttribute({
      type_definition_id: props.typeId,
      internal_name: form.internal_name,
      display_name: form.display_name,
      description: form.description || undefined,
      data_type: form.data_type,
      required: form.required,
      multi_valued: form.multi_valued,
      unique: form.unique,
      constraints,
      ...presentation,
    })
  },
  onSuccess: (a) => {
    queryClient.invalidateQueries({ queryKey: ['attributes', props.typeId] })
    toasts.success(
      props.attribute ? `Saved "${a.display_name}" — now version ${a.version}` : `Attribute "${a.display_name}" created`,
    )
    emit('close')
  },
  onError: (e) => {
    error.value = friendlyError(e)
  },
})

// "Try a value" — dry-run against the saved definition.
const tester = reactive({
  value: '',
  result: null as null | { ok: boolean; message: string },
  busy: false,
})

async function tryValue() {
  if (!props.attribute) return
  tester.busy = true
  tester.result = null
  try {
    const converted = toApiValue(props.attribute.data_type, tester.value)
    await api.validateAttributeValue(props.attribute.id, converted)
    tester.result = { ok: true, message: 'Valid — an API write of this value would succeed.' }
  } catch (e) {
    tester.result = {
      ok: false,
      message: e instanceof ApiError || e instanceof Error ? friendlyError(e) : 'Invalid value.',
    }
  } finally {
    tester.busy = false
  }
}
</script>

<template>
  <Drawer
    :open="open"
    :title="attribute ? `Edit ${attribute.display_name}` : 'New attribute'"
    :subtitle="attribute ? `${attribute.internal_name} · version ${attribute.version}` : undefined"
    @close="emit('close')"
  >
    <form class="flex flex-col gap-4" @submit.prevent="save.mutate()">
      <div v-if="!attribute" class="grid grid-cols-2 gap-3">
        <Input
          v-model="form.internal_name"
          label="Internal name"
          mono
          placeholder="unit_weight_kg"
          hint="snake_case; immutable once created"
          :error="fieldErrors.internal_name"
        />
        <Select
          v-model="form.data_type"
          label="Data type"
          :options="DATA_TYPES.map((d) => ({ value: d, label: d }))"
          hint="Immutable once created"
        />
      </div>

      <Input v-model="form.display_name" label="Display name" placeholder="Unit weight (kg)" />
      <Input v-model="form.description" label="Description" placeholder="Optional" />

      <fieldset class="flex flex-col gap-2.5 rounded-md border border-(--border) p-3">
        <legend class="px-1 text-[13px] font-medium text-(--text-secondary)">Presentation</legend>
        <div class="grid grid-cols-2 gap-3">
          <Input v-model="form.group" label="Group" placeholder="e.g. Pricing" hint="Section this attribute renders in" />
          <Input v-model="form.sortOrder" type="number" label="Order" hint="Position within the group" />
        </div>
        <Input v-model="form.helpText" label="Help text" placeholder="Optional inline guidance for editors" />
      </fieldset>

      <fieldset class="flex flex-col gap-2.5 rounded-md border border-(--border) p-3">
        <legend class="px-1 text-[13px] font-medium text-(--text-secondary)">Rules</legend>
        <Toggle v-model="form.required" label="Required" hint="Entities must hold a value" />
        <Toggle v-model="form.multi_valued" label="Multi-valued" hint="An entity may hold several values" :disabled="form.unique" />
        <Toggle v-model="form.unique" label="Unique" hint="No two entities may share a value" :disabled="form.multi_valued" />
      </fieldset>

      <fieldset class="flex flex-col gap-3 rounded-md border border-(--border) p-3">
        <legend class="px-1 text-[13px] font-medium text-(--text-secondary)">Constraints</legend>

        <div v-if="isTextual" class="grid grid-cols-2 gap-3">
          <Input v-model="form.minLength" type="number" label="Min length" />
          <Input v-model="form.maxLength" type="number" label="Max length" />
        </div>
        <div v-if="isOrdered" class="grid grid-cols-2 gap-3">
          <Input v-model="form.minValue" label="Min value" mono />
          <Input v-model="form.maxValue" label="Max value" mono />
        </div>
        <Input v-if="isTextual" v-model="form.pattern" label="Pattern (RE2)" mono placeholder="^[A-Z]{2}-\d{4}$" />

        <div v-if="isEnum">
          <span class="mb-1 block text-[13px] font-medium text-(--text-secondary)">Allowed members</span>
          <div class="flex flex-wrap gap-1.5">
            <Badge v-for="(m, i) in form.enumMembers" :key="m" tone="accent">
              {{ m }}
              <button type="button" :aria-label="`Remove ${m}`" @click="form.enumMembers.splice(i, 1)">
                <X :size="12" />
              </button>
            </Badge>
          </div>
          <div class="mt-2 flex gap-2">
            <Input v-model="form.newMember" placeholder="Add member…" @keydown.enter.prevent="addMember" />
            <Button @click="addMember"><Plus :size="14" /> Add</Button>
          </div>
        </div>

        <p v-if="!isTextual && !isOrdered && !isEnum" class="text-[13px] text-(--text-muted)">
          {{ form.data_type }} attributes have no additional constraints.
        </p>
      </fieldset>

      <div v-if="attribute" class="rounded-md border border-(--border) bg-(--canvas) p-3">
        <p class="mb-2 flex items-center gap-1.5 text-[13px] font-medium text-(--text-secondary)">
          <FlaskConical :size="14" /> Try a value against the saved definition
        </p>
        <div class="flex items-start gap-2">
          <div class="flex-1">
            <ValueInput
              v-model="tester.value"
              :data-type="attribute.data_type"
              :allowed-values="isEnum ? form.enumMembers : undefined"
            />
          </div>
          <Button :disabled="tester.busy" @click="tryValue">Test</Button>
        </div>
        <p
          v-if="tester.result"
          class="mt-2 flex items-center gap-1.5 text-[13px]"
          :class="tester.result.ok ? 'text-(--ok)' : 'text-(--danger)'"
        >
          <CircleCheck v-if="tester.result.ok" :size="14" />
          <CircleX v-else :size="14" />
          {{ tester.result.message }}
        </p>
        <p v-else class="mt-2 text-[12px] text-(--text-muted)">
          Unsaved edits are not tested — save first to test new rules.
        </p>
      </div>

      <p v-if="error" class="rounded-md bg-(--danger-soft) px-3 py-2 text-[13px] text-(--danger)">{{ error }}</p>
    </form>

    <template #footer>
      <div class="flex items-center justify-between">
        <p v-if="attribute" class="text-[12px] text-(--text-muted)">Saving bumps the definition to version {{ attribute.version + 1 }}.</p>
        <span v-else />
        <div class="flex gap-2">
          <Button @click="emit('close')">Cancel</Button>
          <Button variant="primary" :disabled="save.isPending.value || !canSubmit" @click="save.mutate()">
            {{ attribute ? 'Save changes' : 'Create attribute' }}
          </Button>
        </div>
      </div>
    </template>
  </Drawer>
</template>
