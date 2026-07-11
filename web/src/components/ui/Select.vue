<script setup lang="ts">
defineProps<{
  label?: string
  options: { value: string; label: string }[]
  disabled?: boolean
  hint?: string
}>()

const model = defineModel<string>({ default: '' })
</script>

<template>
  <label class="block">
    <span v-if="label" class="mb-1 block text-[13px] font-medium text-(--text-secondary)">{{ label }}</span>
    <select
      v-model="model"
      :disabled="disabled"
      class="h-8.5 w-full rounded-md border border-(--border-strong) bg-(--surface) px-2 text-sm text-(--text) disabled:opacity-50"
    >
      <option v-for="o in options" :key="o.value" :value="o.value">{{ o.label }}</option>
    </select>
    <span v-if="hint" class="mt-1 block text-[13px] text-(--text-muted)">{{ hint }}</span>
  </label>
</template>
