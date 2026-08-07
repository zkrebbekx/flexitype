<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { api, friendlyError } from '@/lib/api'
import type { AttributeDefinition, Condition, Constraint, DataType, Dependency, TypedValue } from '@/lib/api'
import { renderTyped, typedValue } from '@/lib/values'
import { useToasts } from '@/composables/useToasts'
import Drawer from '@/components/ui/Drawer.vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import Toggle from '@/components/ui/Toggle.vue'
import Badge from '@/components/ui/Badge.vue'
import ValueInput from '@/components/ValueInput.vue'
import { Plus, X, ArrowRight } from 'lucide-vue-next'

const props = defineProps<{
  open: boolean
  typeId: string
  attributes: AttributeDefinition[]
  dependency?: Dependency
}>()
const emit = defineEmits<{ close: [] }>()

const toasts = useToasts()
const queryClient = useQueryClient()

// The backend's own capability gates, mirrored so the builder only offers
// what the source attribute's data type supports (condition.Validate).
const TEXTUAL: DataType[] = ['string', 'enum', 'url', 'email']
const ORDERED: DataType[] = ['integer', 'float', 'decimal', 'date', 'time', 'datetime', 'quantity']
const TEMPORAL: DataType[] = ['date', 'time', 'datetime']

// A range condition is edited as a comparator: 'between' carries both bounds,
// the single-bound comparators encode strictness (> = exclusive min).
type Comparator = 'between' | 'gt' | 'gte' | 'lt' | 'lte'

interface ConditionRow {
  kind: Condition['kind']
  value: string
  values: string[]
  newValue: string
  cmp: Comparator
  min: string
  max: string
  minExclusive: boolean
  maxExclusive: boolean
  pattern: string
  patternSubstring: boolean
  op: NonNullable<Condition['op']>
  dynamicKind: 'now' | 'today' | 'relative_time'
  period: string
  amount: string
}

function emptyCondition(kind: Condition['kind'] = 'equals'): ConditionRow {
  return {
    kind,
    value: '',
    values: [],
    newValue: '',
    cmp: 'between',
    min: '',
    max: '',
    minExclusive: false,
    maxExclusive: false,
    pattern: '',
    patternSubstring: false,
    op: 'before',
    dynamicKind: 'today',
    period: 'days',
    amount: '0',
  }
}

const form = reactive({
  sourceId: '',
  targetId: '',
  description: '',
  conditions: [emptyCondition()],
  allowedValues: [] as string[],
  newAllowed: '',
  requiredOverride: 'none' as 'none' | 'true' | 'false',
  // Extra constraints the effect adds to the target while matched. Which
  // fields render depends on the target's data type, exactly as the
  // attribute drawer's own constraint editor does.
  minLength: '',
  maxLength: '',
  minValue: '',
  maxValue: '',
  pattern: '',
  mime: '',
  maxSize: '',
})
const error = ref('')

const attrOptions = computed(() =>
  props.attributes.filter((a) => !a.archived_at).map((a) => ({ value: a.id, label: `${a.display_name} (${a.data_type})` })),
)
const source = computed(() => props.attributes.find((a) => a.id === form.sourceId))
const target = computed(() => props.attributes.find((a) => a.id === form.targetId))

// Condition kinds the source's data type supports. equals/in work for every
// type; range needs an order, pattern needs text, dynamic needs a timestamp.
const kindOptions = computed(() => {
  const dt = source.value?.data_type
  const kinds = [
    { value: 'equals', label: 'equals' },
    { value: 'in', label: 'is one of' },
  ]
  if (dt && ORDERED.includes(dt)) kinds.push({ value: 'range', label: 'compares to a value' })
  if (dt && TEXTUAL.includes(dt)) kinds.push({ value: 'pattern', label: 'matches pattern' })
  if (dt && TEMPORAL.includes(dt)) kinds.push({ value: 'dynamic', label: 'compared to now/today' })
  return kinds
})

// A source change can strand rows on a kind the new type does not support —
// snap those back to equals rather than submitting a rule the API rejects.
watch(source, () => {
  const allowed = new Set(kindOptions.value.map((k) => k.value))
  for (const row of form.conditions) if (!allowed.has(row.kind)) Object.assign(row, emptyCondition())
})

const targetIsTextual = computed(() => !!target.value && TEXTUAL.includes(target.value.data_type))
const targetIsOrdered = computed(() => !!target.value && ORDERED.includes(target.value.data_type))
const targetIsMedia = computed(() => target.value?.data_type === 'media')

function conditionFromApi(c: Condition): ConditionRow {
  const row = emptyCondition()
  row.kind = c.kind
  if (c.value) row.value = renderTyped(c.value)
  if (c.values) row.values = c.values.map(renderTyped)
  if (c.min) row.min = renderTyped(c.min)
  if (c.max) row.max = renderTyped(c.max)
  row.minExclusive = c.min_exclusive ?? false
  row.maxExclusive = c.max_exclusive ?? false
  if (c.min && c.max) row.cmp = 'between'
  else if (c.min) row.cmp = row.minExclusive ? 'gt' : 'gte'
  else if (c.max) row.cmp = row.maxExclusive ? 'lt' : 'lte'
  row.pattern = c.pattern ?? ''
  row.patternSubstring = c.pattern_substring ?? false
  if (c.op) row.op = c.op
  if (c.dynamic) {
    row.dynamicKind = c.dynamic.kind
    row.period = c.dynamic.period ?? 'days'
    row.amount = String(c.dynamic.amount ?? 0)
  }
  return row
}

watch(
  () => [props.open, props.dependency?.id],
  () => {
    if (!props.open) return
    error.value = ''
    const d = props.dependency
    form.sourceId = d?.source_attribute_id ?? ''
    form.targetId = d?.target_attribute_id ?? ''
    form.description = d?.description ?? ''
    form.conditions = d?.conditions.length ? d.conditions.map(conditionFromApi) : [emptyCondition()]
    form.allowedValues = (d?.effect.allowed_values ?? []).map(renderTyped)
    form.newAllowed = ''
    form.requiredOverride = d?.effect.required === undefined ? 'none' : String(d.effect.required) as 'true' | 'false'
    form.minLength = ''
    form.maxLength = ''
    form.minValue = ''
    form.maxValue = ''
    form.pattern = ''
    form.mime = ''
    form.maxSize = ''
    for (const c of d?.effect.constraints ?? []) {
      if (c.kind === 'min_length') form.minLength = String(c.n)
      if (c.kind === 'max_length') form.maxLength = String(c.n)
      if (c.kind === 'min_value' && c.value) form.minValue = renderTyped(c.value)
      if (c.kind === 'max_value' && c.value) form.maxValue = renderTyped(c.value)
      if (c.kind === 'pattern') form.pattern = c.expr ?? ''
      if (c.kind === 'media') {
        form.mime = (c.mime ?? []).join(', ')
        form.maxSize = c.max_size ? String(c.max_size) : ''
      }
    }
  },
  { immediate: true },
)

// Enum members of an attribute, for pick-lists in the builder. A bool gets a
// fixed pick-list so free text never reaches a two-valued type.
function membersOf(attr?: AttributeDefinition): string[] | undefined {
  if (attr?.data_type === 'bool') return ['true', 'false']
  const oneOf = attr?.constraints.find((c) => c.kind === 'one_of')
  return oneOf?.values?.map(renderTyped)
}

function buildConditions(): Condition[] {
  if (!source.value) throw new Error('pick a source attribute')
  const dt = source.value.data_type
  return form.conditions.map((row): Condition => {
    switch (row.kind) {
      case 'equals':
        return { kind: 'equals', value: typedValue(dt, row.value) }
      case 'in':
        return { kind: 'in', values: row.values.map((v) => typedValue(dt, v)) }
      case 'range': {
        const c: Condition = { kind: 'range' }
        if (row.cmp === 'between' || row.cmp === 'gt' || row.cmp === 'gte') {
          c.min = typedValue(dt, row.min)
          if (row.cmp === 'gt' || (row.cmp === 'between' && row.minExclusive)) c.min_exclusive = true
        }
        if (row.cmp === 'between' || row.cmp === 'lt' || row.cmp === 'lte') {
          c.max = typedValue(dt, row.max)
          if (row.cmp === 'lt' || (row.cmp === 'between' && row.maxExclusive)) c.max_exclusive = true
        }
        return c
      }
      case 'pattern':
        return { kind: 'pattern', pattern: row.pattern, pattern_substring: row.patternSubstring || undefined }
      case 'dynamic':
        return {
          kind: 'dynamic',
          op: row.op,
          dynamic:
            row.dynamicKind === 'relative_time'
              ? { kind: 'relative_time', period: row.period, amount: Number(row.amount) }
              : { kind: row.dynamicKind },
        }
    }
  })
}

function buildEffectConstraints(): Constraint[] | undefined {
  if (!target.value) return undefined
  const dt = target.value.data_type
  const cs: Constraint[] = []
  if (targetIsTextual.value && form.minLength) cs.push({ kind: 'min_length', n: Number(form.minLength) })
  if (targetIsTextual.value && form.maxLength) cs.push({ kind: 'max_length', n: Number(form.maxLength) })
  if (targetIsOrdered.value && form.minValue) cs.push({ kind: 'min_value', value: typedValue(dt, form.minValue) })
  if (targetIsOrdered.value && form.maxValue) cs.push({ kind: 'max_value', value: typedValue(dt, form.maxValue) })
  if (targetIsTextual.value && form.pattern) cs.push({ kind: 'pattern', expr: form.pattern })
  if (targetIsMedia.value && (form.mime.trim() || form.maxSize)) {
    const mime = form.mime.split(',').map((m) => m.trim()).filter(Boolean)
    cs.push({ kind: 'media', mime: mime.length ? mime : undefined, max_size: form.maxSize ? Number(form.maxSize) : undefined })
  }
  return cs.length ? cs : undefined
}

const save = useMutation({
  mutationFn: async () => {
    if (!target.value) throw new Error('pick a target attribute')
    const allowed: TypedValue[] = form.allowedValues.map((v) => typedValue(target.value!.data_type, v))
    const effect = {
      allowed_values: allowed.length ? allowed : undefined,
      constraints: buildEffectConstraints(),
      required: form.requiredOverride === 'none' ? undefined : form.requiredOverride === 'true',
    }
    const conditions = buildConditions()
    if (props.dependency) {
      return api.updateDependency(props.dependency.id, { conditions, effect, description: form.description || undefined })
    }
    return api.createDependency({
      source_attribute_id: form.sourceId,
      target_attribute_id: form.targetId,
      conditions,
      effect,
      description: form.description || undefined,
    })
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['dependencies'] })
    toasts.success(props.dependency ? 'Dependency updated' : 'Dependency created')
    emit('close')
  },
  onError: (e) => {
    error.value = friendlyError(e)
  },
})

const COMPARATORS = [
  { value: 'between', label: 'is between' },
  { value: 'gt', label: 'is greater than' },
  { value: 'gte', label: 'is at least (≥)' },
  { value: 'lt', label: 'is less than' },
  { value: 'lte', label: 'is at most (≤)' },
]
</script>

<template>
  <Drawer
    :open="open"
    :title="dependency ? 'Edit dependency' : 'New dependency'"
    subtitle="When the source matches every condition, the effect applies to the target."
    @close="emit('close')"
  >
    <form class="flex flex-col gap-4" @submit.prevent="save.mutate()">
      <div class="grid grid-cols-[1fr_auto_1fr] items-end gap-2">
        <Select
          v-model="form.sourceId"
          label="Source attribute"
          :disabled="!!dependency"
          :options="[{ value: '', label: 'Select…' }, ...attrOptions]"
        />
        <ArrowRight :size="16" class="mb-2.5 text-(--text-muted)" />
        <Select
          v-model="form.targetId"
          label="Target attribute"
          :disabled="!!dependency"
          :options="[{ value: '', label: 'Select…' }, ...attrOptions.filter((o) => o.value !== form.sourceId)]"
        />
      </div>

      <fieldset class="flex flex-col gap-3 rounded-md border border-(--border) p-3">
        <legend class="px-1 text-[13px] font-medium text-(--text-secondary)">
          Conditions on {{ source?.display_name ?? 'source' }} (all must match)
        </legend>

        <p v-if="!source" class="text-[13px] text-(--text-muted)">Pick a source attribute first — its data type decides which conditions apply.</p>

        <div
          v-for="(row, i) in form.conditions"
          v-else
          :key="i"
          class="flex flex-col gap-2 rounded-md border border-(--border) bg-(--canvas) p-2.5"
        >
          <div class="flex items-center gap-2">
            <Select v-model="row.kind" :options="kindOptions" class="flex-1" />
            <button
              v-if="form.conditions.length > 1"
              type="button"
              class="text-(--text-muted) hover:text-(--danger)"
              aria-label="Remove condition"
              @click="form.conditions.splice(i, 1)"
            >
              <X :size="15" />
            </button>
          </div>

          <ValueInput
            v-if="row.kind === 'equals'"
            v-model="row.value"
            :data-type="source.data_type"
            :allowed-values="membersOf(source)"
            :unit-family-id="source.unit_family_id"
            :display-unit="source.display_unit"
          />

          <div v-else-if="row.kind === 'in'">
            <div class="flex flex-wrap gap-1.5">
              <Badge v-for="(v, vi) in row.values" :key="v" tone="accent">
                {{ v }}
                <button type="button" :aria-label="`Remove ${v}`" @click="row.values.splice(vi, 1)"><X :size="12" /></button>
              </Badge>
            </div>
            <div class="mt-1.5 flex items-start gap-2">
              <div class="flex-1">
                <ValueInput
                  v-model="row.newValue"
                  :data-type="source.data_type"
                  :allowed-values="membersOf(source)"
                  :unit-family-id="source.unit_family_id"
                  :display-unit="source.display_unit"
                />
              </div>
              <Button size="sm" @click="row.newValue.trim() && !row.values.includes(row.newValue.trim()) && (row.values.push(row.newValue.trim()), (row.newValue = ''))"><Plus :size="13" /></Button>
            </div>
          </div>

          <div v-else-if="row.kind === 'range'" class="flex flex-col gap-2">
            <Select v-model="row.cmp" :options="COMPARATORS" />
            <div v-if="row.cmp === 'between'" class="grid grid-cols-2 gap-2">
              <ValueInput
                v-model="row.min"
                label="From"
                :data-type="source.data_type"
                :unit-family-id="source.unit_family_id"
                :display-unit="source.display_unit"
              />
              <ValueInput
                v-model="row.max"
                label="To"
                :data-type="source.data_type"
                :unit-family-id="source.unit_family_id"
                :display-unit="source.display_unit"
              />
              <Toggle v-model="row.minExclusive" label="Exclude the From value" />
              <Toggle v-model="row.maxExclusive" label="Exclude the To value" />
            </div>
            <ValueInput
              v-else
              :model-value="row.cmp === 'gt' || row.cmp === 'gte' ? row.min : row.max"
              :data-type="source.data_type"
              :unit-family-id="source.unit_family_id"
              :display-unit="source.display_unit"
              @update:model-value="(v: string) => (row.cmp === 'gt' || row.cmp === 'gte' ? (row.min = v) : (row.max = v))"
            />
          </div>

          <div v-else-if="row.kind === 'pattern'" class="flex flex-col gap-2">
            <Input v-model="row.pattern" label="RE2 pattern" mono placeholder="^[A-Z]{2}-\d{4}$" />
            <Toggle v-model="row.patternSubstring" label="Match anywhere in the value" hint="Off = the whole value must match" />
          </div>

          <div v-else-if="row.kind === 'dynamic'" class="grid grid-cols-2 gap-2">
            <Select
              v-model="row.op"
              label="Source value is"
              :options="[
                { value: 'before', label: 'before' },
                { value: 'after', label: 'after' },
                { value: 'on_or_before', label: 'on or before' },
                { value: 'on_or_after', label: 'on or after' },
              ]"
            />
            <Select
              v-model="row.dynamicKind"
              label="Reference"
              :options="[
                { value: 'today', label: 'today' },
                { value: 'now', label: 'now' },
                { value: 'relative_time', label: 'now ± offset' },
              ]"
            />
            <template v-if="row.dynamicKind === 'relative_time'">
              <Input v-model="row.amount" type="number" label="Amount (± allowed)" />
              <Select
                v-model="row.period"
                label="Period"
                :options="['seconds', 'minutes', 'hours', 'days', 'weeks'].map((p) => ({ value: p, label: p }))"
              />
            </template>
          </div>
        </div>

        <Button v-if="source" size="sm" @click="form.conditions.push(emptyCondition())"><Plus :size="13" /> Add condition</Button>
      </fieldset>

      <fieldset class="flex flex-col gap-3 rounded-md border border-(--border) p-3">
        <legend class="px-1 text-[13px] font-medium text-(--text-secondary)">
          Effect on {{ target?.display_name ?? 'target' }}
        </legend>

        <p v-if="!target" class="text-[13px] text-(--text-muted)">Pick a target attribute first — its data type decides which effects apply.</p>

        <template v-else>
          <div>
            <span class="mb-1 block text-[13px] font-medium text-(--text-secondary)">Narrow allowed values to</span>
            <div class="flex flex-wrap gap-1.5">
              <Badge v-for="(v, vi) in form.allowedValues" :key="v" tone="ok">
                {{ v }}
                <button type="button" :aria-label="`Remove ${v}`" @click="form.allowedValues.splice(vi, 1)"><X :size="12" /></button>
              </Badge>
            </div>
            <div class="mt-1.5 flex items-start gap-2">
              <div class="flex-1">
                <ValueInput
                  v-model="form.newAllowed"
                  :data-type="target.data_type"
                  :allowed-values="membersOf(target)"
                  :unit-family-id="target.unit_family_id"
                  :display-unit="target.display_unit"
                />
              </div>
              <Button size="sm" @click="form.newAllowed.trim() && !form.allowedValues.includes(form.newAllowed.trim()) && (form.allowedValues.push(form.newAllowed.trim()), (form.newAllowed = ''))"><Plus :size="13" /></Button>
            </div>
            <p class="mt-1 text-[12px] text-(--text-muted)">Leave empty to keep the target's own allowed set.</p>
          </div>

          <Select
            v-model="form.requiredOverride"
            label="Required override"
            :options="[
              { value: 'none', label: 'No override' },
              { value: 'true', label: 'Force required' },
              { value: 'false', label: 'Force optional' },
            ]"
          />

          <div v-if="targetIsTextual || targetIsOrdered || targetIsMedia" class="flex flex-col gap-2">
            <span class="text-[13px] font-medium text-(--text-secondary)">Extra constraints while matched</span>
            <div v-if="targetIsTextual" class="grid grid-cols-2 gap-2">
              <Input v-model="form.minLength" type="number" label="Min length" />
              <Input v-model="form.maxLength" type="number" label="Max length" />
            </div>
            <div v-if="targetIsOrdered" class="grid grid-cols-2 gap-2">
              <ValueInput
                v-model="form.minValue"
                label="Min value"
                :data-type="target.data_type"
                :unit-family-id="target.unit_family_id"
                :display-unit="target.display_unit"
              />
              <ValueInput
                v-model="form.maxValue"
                label="Max value"
                :data-type="target.data_type"
                :unit-family-id="target.unit_family_id"
                :display-unit="target.display_unit"
              />
            </div>
            <Input v-if="targetIsTextual" v-model="form.pattern" label="Pattern (RE2)" mono placeholder="^[A-Z]{2}-\d{4}$" />
            <div v-if="targetIsMedia" class="grid grid-cols-2 gap-2">
              <Input v-model="form.mime" label="Allowed MIME types" placeholder="image/png, image/jpeg" hint="Comma-separated; empty accepts any" />
              <Input v-model="form.maxSize" type="number" label="Max size (bytes)" hint="Empty is unbounded" />
            </div>
          </div>
        </template>
      </fieldset>

      <Input v-model="form.description" label="Description" placeholder="Why this rule exists (optional)" />

      <p v-if="error" class="rounded-md bg-(--danger-soft) px-3 py-2 text-[13px] text-(--danger)">{{ error }}</p>
    </form>

    <template #footer>
      <div class="flex justify-end gap-2">
        <Button @click="emit('close')">Cancel</Button>
        <Button variant="primary" :disabled="save.isPending.value" @click="save.mutate()">
          {{ dependency ? 'Save changes' : 'Create dependency' }}
        </Button>
      </div>
    </template>
  </Drawer>
</template>
