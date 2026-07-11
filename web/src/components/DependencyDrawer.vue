<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { api, friendlyError } from '@/lib/api'
import type { AttributeDefinition, Condition, Dependency, TypedValue } from '@/lib/api'
import { renderTyped, typedValue } from '@/lib/values'
import { useToasts } from '@/composables/useToasts'
import Drawer from '@/components/ui/Drawer.vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
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

interface ConditionRow {
  kind: Condition['kind']
  value: string
  values: string[]
  newValue: string
  min: string
  max: string
  pattern: string
  op: NonNullable<Condition['op']>
  dynamicKind: 'now' | 'today' | 'relative_time'
  period: string
  amount: string
}

function emptyCondition(): ConditionRow {
  return {
    kind: 'equals',
    value: '',
    values: [],
    newValue: '',
    min: '',
    max: '',
    pattern: '',
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
})
const error = ref('')

const attrOptions = computed(() =>
  props.attributes.filter((a) => !a.archived_at).map((a) => ({ value: a.id, label: `${a.display_name} (${a.data_type})` })),
)
const source = computed(() => props.attributes.find((a) => a.id === form.sourceId))
const target = computed(() => props.attributes.find((a) => a.id === form.targetId))

function conditionFromApi(c: Condition): ConditionRow {
  const row = emptyCondition()
  row.kind = c.kind
  if (c.value) row.value = renderTyped(c.value)
  if (c.values) row.values = c.values.map(renderTyped)
  if (c.min) row.min = renderTyped(c.min)
  if (c.max) row.max = renderTyped(c.max)
  row.pattern = c.pattern ?? ''
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
  },
  { immediate: true },
)

// Enum members of an attribute, for pick-lists in the builder.
function membersOf(attr?: AttributeDefinition): string[] | undefined {
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
        if (row.min) c.min = typedValue(dt, row.min)
        if (row.max) c.max = typedValue(dt, row.max)
        return c
      }
      case 'pattern':
        return { kind: 'pattern', pattern: row.pattern }
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

const save = useMutation({
  mutationFn: async () => {
    if (!target.value) throw new Error('pick a target attribute')
    const allowed: TypedValue[] = form.allowedValues.map((v) => typedValue(target.value!.data_type, v))
    const effect = {
      allowed_values: allowed.length ? allowed : undefined,
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

const CONDITION_KINDS = [
  { value: 'equals', label: 'equals' },
  { value: 'in', label: 'is one of' },
  { value: 'range', label: 'is in range' },
  { value: 'pattern', label: 'matches pattern' },
  { value: 'dynamic', label: 'compared to now/today' },
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

        <div
          v-for="(row, i) in form.conditions"
          :key="i"
          class="flex flex-col gap-2 rounded-md border border-(--border) bg-(--canvas) p-2.5"
        >
          <div class="flex items-center gap-2">
            <Select v-model="row.kind" :options="CONDITION_KINDS" class="flex-1" />
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
            v-if="row.kind === 'equals' && source"
            v-model="row.value"
            :data-type="source.data_type"
            :allowed-values="membersOf(source)"
          />

          <div v-else-if="row.kind === 'in'">
            <div class="flex flex-wrap gap-1.5">
              <Badge v-for="(v, vi) in row.values" :key="v" tone="accent">
                {{ v }}
                <button type="button" :aria-label="`Remove ${v}`" @click="row.values.splice(vi, 1)"><X :size="12" /></button>
              </Badge>
            </div>
            <div class="mt-1.5 flex gap-2">
              <Input v-model="row.newValue" placeholder="Add value…" @keydown.enter.prevent="row.newValue.trim() && (row.values.push(row.newValue.trim()), (row.newValue = ''))" />
              <Button size="sm" @click="row.newValue.trim() && (row.values.push(row.newValue.trim()), (row.newValue = ''))"><Plus :size="13" /></Button>
            </div>
          </div>

          <div v-else-if="row.kind === 'range'" class="grid grid-cols-2 gap-2">
            <Input v-model="row.min" label="Min (inclusive)" mono />
            <Input v-model="row.max" label="Max (inclusive)" mono />
          </div>

          <Input v-else-if="row.kind === 'pattern'" v-model="row.pattern" label="RE2 pattern" mono />

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

        <Button size="sm" @click="form.conditions.push(emptyCondition())"><Plus :size="13" /> Add condition</Button>
      </fieldset>

      <fieldset class="flex flex-col gap-3 rounded-md border border-(--border) p-3">
        <legend class="px-1 text-[13px] font-medium text-(--text-secondary)">
          Effect on {{ target?.display_name ?? 'target' }}
        </legend>

        <div>
          <span class="mb-1 block text-[13px] font-medium text-(--text-secondary)">Narrow allowed values to</span>
          <div class="flex flex-wrap gap-1.5">
            <Badge v-for="(v, vi) in form.allowedValues" :key="v" tone="ok">
              {{ v }}
              <button type="button" :aria-label="`Remove ${v}`" @click="form.allowedValues.splice(vi, 1)"><X :size="12" /></button>
            </Badge>
          </div>
          <div class="mt-1.5 flex gap-2">
            <template v-if="membersOf(target)">
              <Select
                v-model="form.newAllowed"
                :options="[{ value: '', label: 'Add member…' }, ...(membersOf(target) ?? []).filter((m) => !form.allowedValues.includes(m)).map((m) => ({ value: m, label: m }))]"
              />
              <Button size="sm" @click="form.newAllowed && (form.allowedValues.push(form.newAllowed), (form.newAllowed = ''))"><Plus :size="13" /></Button>
            </template>
            <template v-else>
              <Input v-model="form.newAllowed" placeholder="Add value…" @keydown.enter.prevent="form.newAllowed.trim() && (form.allowedValues.push(form.newAllowed.trim()), (form.newAllowed = ''))" />
              <Button size="sm" @click="form.newAllowed.trim() && (form.allowedValues.push(form.newAllowed.trim()), (form.newAllowed = ''))"><Plus :size="13" /></Button>
            </template>
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
