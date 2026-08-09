<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { api, DATA_TYPES, friendlyError } from '@/lib/api'
import type { AttributeDefinition, Condition, DataType, Dependency } from '@/lib/api'
import { renderTyped } from '@/lib/values'
import {
  ORDERED,
  TEMPORAL,
  TEXTUAL,
  buildCondition,
  buildEffect,
  conditionFromApi,
  conditionSubjectType,
  effectFormFromApi,
  emptyCondition,
  emptyEffectPassthrough,
} from '@/lib/dependency-edit'
import type { ConditionRow, EffectPassthrough } from '@/lib/dependency-edit'
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

const form = reactive({
  sourceId: '',
  targetId: '',
  description: '',
  conditions: [emptyCondition()] as ConditionRow[],
  allowedValues: [] as string[],
  newAllowed: '',
  requiredOverride: 'none' as 'none' | 'true' | 'false',
  // Where the demand is enforced. Only rendered while the override forces
  // required: it is the only effect whose subject can be absent.
  enforce: 'on_read' as 'on_read' | 'on_write',
  // Extra constraints the effect adds to the target while matched. Which
  // fields render depends on the target's data type, exactly as the
  // attribute drawer's own constraint editor does.
  oneOf: [] as string[],
  newOneOf: '',
  minLength: '',
  maxLength: '',
  minValue: '',
  maxValue: '',
  pattern: '',
  patternSubstring: false,
  mime: '',
  maxSize: '',
})
// What the loaded effect carries beyond the form fields. buildEffect
// re-attaches it on save so an edit cannot silently subtract it.
const effectPassthrough = ref<EffectPassthrough>(emptyEffectPassthrough())
const error = ref('')

const attrOptions = computed(() =>
  props.attributes.filter((a) => !a.archived_at).map((a) => ({ value: a.id, label: `${a.display_name} (${a.data_type})` })),
)
const source = computed(() => props.attributes.find((a) => a.id === form.sourceId))
const target = computed(() => props.attributes.find((a) => a.id === form.targetId))

// subjectTypeOf returns the data type a row's operands validate against:
// the declared fact type for a context condition, else the source's type.
// The 'string' fallback is unreachable in the template: the condition rows
// render only after the user picks a source.
function subjectTypeOf(row: ConditionRow): DataType {
  return conditionSubjectType(row, source.value?.data_type ?? 'string')
}

// Condition kinds the row's subject data type supports. equals/in work for
// every type; range needs an order, pattern needs text, dynamic needs a
// timestamp.
function kindOptionsFor(row: ConditionRow) {
  const dt = subjectTypeOf(row)
  const kinds = [
    { value: 'equals', label: 'equals' },
    { value: 'in', label: 'is one of' },
  ]
  if (dt && ORDERED.includes(dt)) kinds.push({ value: 'range', label: 'compares to a value' })
  if (dt && TEXTUAL.includes(dt)) kinds.push({ value: 'pattern', label: 'matches pattern' })
  if (dt && TEMPORAL.includes(dt)) kinds.push({ value: 'dynamic', label: 'compared to now/today' })
  return kinds
}

// A subject-type change can strand a row on a kind the new type does not
// support — snap the kind back to equals rather than submitting a rule the
// API rejects.
function snapKind(row: ConditionRow) {
  const allowed = new Set(kindOptionsFor(row).map((k) => k.value))
  if (!allowed.has(row.kind)) row.kind = 'equals'
}

function setUseContext(row: ConditionRow, on: boolean) {
  row.useContext = on
  snapKind(row)
}

function setContextType(row: ConditionRow, dt: DataType) {
  row.contextType = dt
  snapKind(row)
}

watch(source, () => {
  for (const row of form.conditions) {
    if (row.useContext) continue
    const allowed = new Set(kindOptionsFor(row).map((k) => k.value))
    if (!allowed.has(row.kind)) Object.assign(row, emptyCondition())
  }
})

const targetIsTextual = computed(() => !!target.value && TEXTUAL.includes(target.value.data_type))
const targetIsOrdered = computed(() => !!target.value && ORDERED.includes(target.value.data_type))
const targetIsMedia = computed(() => target.value?.data_type === 'media')

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
    const loaded = effectFormFromApi(d?.effect, target.value?.data_type)
    Object.assign(form, loaded.form)
    effectPassthrough.value = loaded.passthrough
    form.newAllowed = ''
    form.newOneOf = ''
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
  return form.conditions.map((row) => buildCondition(row, dt))
}

const save = useMutation({
  mutationFn: async () => {
    if (!target.value) throw new Error('pick a target attribute')
    const effect = buildEffect(form, target.value.data_type, effectPassthrough.value)
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
            <Select v-model="row.kind" :options="kindOptionsFor(row)" class="flex-1" />
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

          <Toggle
            :model-value="row.useContext"
            label="Test a caller-supplied fact"
            hint="The condition reads a request fact instead of the source value"
            @update:model-value="(v: boolean) => setUseContext(row, v)"
          />
          <div v-if="row.useContext" class="grid grid-cols-2 gap-2">
            <Input v-model="row.contextKey" label="Fact key" mono placeholder="tier" />
            <Select
              :model-value="row.contextType"
              label="Fact data type"
              :options="DATA_TYPES.map((d) => ({ value: d, label: d }))"
              @update:model-value="(v: string) => setContextType(row, v as DataType)"
            />
          </div>

          <ValueInput
            v-if="row.kind === 'equals'"
            v-model="row.value"
            :data-type="subjectTypeOf(row)"
            :allowed-values="row.useContext ? undefined : membersOf(source)"
            :unit-family-id="row.useContext ? undefined : source.unit_family_id"
            :display-unit="row.useContext ? undefined : source.display_unit"
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
                  :data-type="subjectTypeOf(row)"
                  :allowed-values="row.useContext ? undefined : membersOf(source)"
                  :unit-family-id="row.useContext ? undefined : source.unit_family_id"
                  :display-unit="row.useContext ? undefined : source.display_unit"
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
                :data-type="subjectTypeOf(row)"
                :unit-family-id="row.useContext ? undefined : source.unit_family_id"
                :display-unit="row.useContext ? undefined : source.display_unit"
              />
              <ValueInput
                v-model="row.max"
                label="To"
                :data-type="subjectTypeOf(row)"
                :unit-family-id="row.useContext ? undefined : source.unit_family_id"
                :display-unit="row.useContext ? undefined : source.display_unit"
              />
              <Toggle v-model="row.minExclusive" label="Exclude the From value" />
              <Toggle v-model="row.maxExclusive" label="Exclude the To value" />
            </div>
            <ValueInput
              v-else
              :model-value="row.cmp === 'gt' || row.cmp === 'gte' ? row.min : row.max"
              :data-type="subjectTypeOf(row)"
              :unit-family-id="row.useContext ? undefined : source.unit_family_id"
              :display-unit="row.useContext ? undefined : source.display_unit"
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

          <div v-if="form.requiredOverride === 'true'" class="flex flex-col gap-1">
            <Select
              v-model="form.enforce"
              label="Enforce the requirement"
              :options="[
                { value: 'on_read', label: 'On read — report the gap' },
                { value: 'on_write', label: 'On write — refuse the write' },
              ]"
            />
            <p class="text-[12px] text-(--text-secondary)">
              {{
                form.enforce === 'on_write'
                  ? 'A write that leaves the value missing is refused. The check runs at the end of the write, so a batch or an import row supplying both together passes in any order — but a caller writing one attribute per request cannot set the condition first.'
                  : 'The write is accepted and the gap is reported by completeness, for the application to act on where it decides — at publish, at checkout, at submit.'
              }}
            </p>
          </div>

          <div class="flex flex-col gap-2">
            <span class="text-[13px] font-medium text-(--text-secondary)">Extra constraints while matched</span>
            <div>
              <span class="mb-1 block text-[13px] font-medium text-(--text-secondary)">Value must be one of</span>
              <div class="flex flex-wrap gap-1.5">
                <Badge v-for="(v, vi) in form.oneOf" :key="v" tone="accent">
                  {{ v }}
                  <button type="button" :aria-label="`Remove ${v}`" @click="form.oneOf.splice(vi, 1)"><X :size="12" /></button>
                </Badge>
              </div>
              <div class="mt-1.5 flex items-start gap-2">
                <div class="flex-1">
                  <ValueInput
                    v-model="form.newOneOf"
                    :data-type="target.data_type"
                    :allowed-values="membersOf(target)"
                    :unit-family-id="target.unit_family_id"
                    :display-unit="target.display_unit"
                  />
                </div>
                <Button size="sm" @click="form.newOneOf.trim() && !form.oneOf.includes(form.newOneOf.trim()) && (form.oneOf.push(form.newOneOf.trim()), (form.newOneOf = ''))"><Plus :size="13" /></Button>
              </div>
              <p class="mt-1 text-[12px] text-(--text-muted)">Adds a one-of constraint on the target's value. Leave empty for none.</p>
            </div>
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
            <template v-if="targetIsTextual">
              <Input v-model="form.pattern" label="Pattern (RE2)" mono placeholder="^[A-Z]{2}-\d{4}$" />
              <Toggle v-model="form.patternSubstring" label="Match anywhere in the value" hint="Off = the whole value must match" />
            </template>
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
