<script setup lang="ts">
import { computed, useId, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { api } from '@/lib/api'
import type { DataType } from '@/lib/api'
import { inputKind, parseQuantity } from '@/lib/values'
import Toggle from '@/components/ui/Toggle.vue'
import Select from '@/components/ui/Select.vue'

// One control for any data type; when allowedValues is set (enum members
// or a dependency-narrowed set) it renders as a select regardless of type.
// A quantity attribute pins a unit family (unitFamilyId): the magnitude gets a
// numeric input and the unit a dropdown of that family's units.
const props = defineProps<{
  dataType: DataType
  label?: string
  allowedValues?: string[]
  error?: string
  unitFamilyId?: string
  displayUnit?: string
}>()

const model = defineModel<string>({ default: '' })
const boolModel = computed({
  get: () => model.value === 'true',
  set: (v: boolean) => (model.value = String(v)),
})

const errorId = useId()
const kind = computed(() => inputKind(props.dataType))
const nativeType = computed(
  () =>
    ({ number: 'number', date: 'date', time: 'time', datetime: 'datetime-local', text: 'text' })[
      kind.value as 'number' | 'date' | 'time' | 'datetime' | 'text'
    ] ?? 'text',
)

// --- quantity -----------------------------------------------------------------

// Load the attribute's unit family so the unit dropdown offers exactly its
// members; the family lists each unit symbol against its conversion factor.
const familyId = computed(() => props.unitFamilyId)
const family = useQuery({
  queryKey: ['unit-family', familyId],
  queryFn: () => api.getUnitFamily(props.unitFamilyId!),
  enabled: computed(() => props.dataType === 'quantity' && !!props.unitFamilyId),
})
const unitOptions = computed(() => Object.keys(family.data.value?.units ?? {}))

// The model carries the quantity as JSON; expose its two parts as v-models.
const magnitude = computed({
  get: () => parseQuantity(model.value).magnitude,
  set: (m: string) => (model.value = JSON.stringify({ ...parseQuantity(model.value), magnitude: String(m) })),
})
const unit = computed({
  get: () => parseQuantity(model.value).unit,
  set: (u: string) => (model.value = JSON.stringify({ ...parseQuantity(model.value), unit: u })),
})

// Default the unit once the family is known: prefer the attribute's display
// unit, then the base unit, then the first member — but never clobber a unit
// the value already carries (editing an existing value).
watch(
  [unitOptions, () => props.displayUnit],
  () => {
    if (props.dataType !== 'quantity') return
    const opts = unitOptions.value
    if (!opts.length) return
    if (unit.value && opts.includes(unit.value)) return
    const base = family.data.value?.base_unit
    unit.value =
      props.displayUnit && opts.includes(props.displayUnit)
        ? props.displayUnit
        : base && opts.includes(base)
          ? base
          : opts[0]
  },
  { immediate: true },
)
</script>

<template>
  <div>
    <Select
      v-if="allowedValues"
      v-model="model"
      :label="label"
      :options="[{ value: '', label: 'Select…' }, ...allowedValues.map((v) => ({ value: v, label: v }))]"
    />
    <Toggle v-else-if="kind === 'bool'" v-model="boolModel" :label="label ?? 'Value'" />
    <label v-else-if="kind === 'json'" class="block">
      <span v-if="label" class="mb-1 block text-[13px] font-medium text-(--text-secondary)">{{ label }}</span>
      <textarea
        v-model="model"
        rows="5"
        :aria-invalid="error ? 'true' : undefined"
        :aria-describedby="error ? errorId : undefined"
        class="mono w-full rounded-md border bg-(--surface) p-2.5 text-[12.5px]"
        :class="error ? 'border-(--danger)' : 'border-(--border-strong)'"
        placeholder='{ "key": "value" }'
      />
    </label>
    <div v-else-if="kind === 'quantity'">
      <span v-if="label" class="mb-1 block text-[13px] font-medium text-(--text-secondary)">{{ label }}</span>
      <div class="flex items-start gap-2">
        <input
          v-model="magnitude"
          type="text"
          inputmode="decimal"
          aria-label="Magnitude"
          :aria-invalid="error ? 'true' : undefined"
          :aria-describedby="error ? errorId : undefined"
          class="h-8.5 min-w-0 flex-1 rounded-md border bg-(--surface) px-2.5 text-sm"
          :class="error ? 'border-(--danger)' : 'border-(--border-strong)'"
          placeholder="2.5"
        />
        <div class="w-32 shrink-0">
          <Select v-model="unit" aria-label="Unit" :options="unitOptions.map((u) => ({ value: u, label: u }))" />
        </div>
      </div>
      <p v-if="unitFamilyId && !unitOptions.length" class="mt-1 text-[13px] text-(--text-muted)">
        Loading units…
      </p>
      <p v-else-if="!unitFamilyId" class="mt-1 text-[13px] text-(--text-muted)">
        This attribute has no unit family configured.
      </p>
    </div>
    <label v-else class="block">
      <span v-if="label" class="mb-1 block text-[13px] font-medium text-(--text-secondary)">{{ label }}</span>
      <input
        v-model="model"
        :type="nativeType"
        :step="dataType === 'float' || dataType === 'decimal' ? 'any' : undefined"
        :aria-invalid="error ? 'true' : undefined"
        :aria-describedby="error ? errorId : undefined"
        class="h-8.5 w-full rounded-md border bg-(--surface) px-2.5 text-sm"
        :class="error ? 'border-(--danger)' : 'border-(--border-strong)'"
      />
    </label>
    <p v-if="error" :id="errorId" role="alert" class="mt-1 text-[13px] text-(--danger)">{{ error }}</p>
  </div>
</template>
