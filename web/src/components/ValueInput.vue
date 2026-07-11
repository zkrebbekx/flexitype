<script setup lang="ts">
import { computed } from 'vue'
import type { DataType } from '@/lib/api'
import { inputKind } from '@/lib/values'
import Toggle from '@/components/ui/Toggle.vue'
import Select from '@/components/ui/Select.vue'

// One control for any data type; when allowedValues is set (enum members
// or a dependency-narrowed set) it renders as a select regardless of type.
const props = defineProps<{
  dataType: DataType
  label?: string
  allowedValues?: string[]
  error?: string
}>()

const model = defineModel<string>({ default: '' })
const boolModel = computed({
  get: () => model.value === 'true',
  set: (v: boolean) => (model.value = String(v)),
})

const kind = computed(() => inputKind(props.dataType))
const nativeType = computed(
  () =>
    ({ number: 'number', date: 'date', time: 'time', datetime: 'datetime-local', text: 'text' })[
      kind.value as 'number' | 'date' | 'time' | 'datetime' | 'text'
    ] ?? 'text',
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
        class="mono w-full rounded-md border bg-(--surface) p-2.5 text-[12.5px]"
        :class="error ? 'border-(--danger)' : 'border-(--border-strong)'"
        placeholder='{ "key": "value" }'
      />
    </label>
    <label v-else class="block">
      <span v-if="label" class="mb-1 block text-[13px] font-medium text-(--text-secondary)">{{ label }}</span>
      <input
        v-model="model"
        :type="nativeType"
        :step="dataType === 'float' || dataType === 'decimal' ? 'any' : undefined"
        class="h-8.5 w-full rounded-md border bg-(--surface) px-2.5 text-sm"
        :class="error ? 'border-(--danger)' : 'border-(--border-strong)'"
      />
    </label>
    <p v-if="error" class="mt-1 text-[13px] text-(--danger)">{{ error }}</p>
  </div>
</template>
