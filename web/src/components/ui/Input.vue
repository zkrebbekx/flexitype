<script setup lang="ts">
defineProps<{
  label?: string
  hint?: string
  error?: string
  type?: string
  placeholder?: string
  disabled?: boolean
  mono?: boolean
}>()

const model = defineModel<string>({ default: '' })
</script>

<template>
  <label class="block">
    <span v-if="label" class="mb-1 block text-[13px] font-medium text-(--text-secondary)">{{ label }}</span>
    <input
      v-model="model"
      :type="type ?? 'text'"
      :placeholder="placeholder"
      :disabled="disabled"
      class="h-8.5 w-full rounded-md border bg-(--surface) px-2.5 text-sm text-(--text) placeholder:text-(--text-muted) disabled:opacity-50"
      :class="[error ? 'border-(--danger)' : 'border-(--border-strong)', mono ? 'mono' : '']"
    />
    <span v-if="error" class="mt-1 block text-[13px] text-(--danger)">{{ error }}</span>
    <span v-else-if="hint" class="mt-1 block text-[13px] text-(--text-muted)">{{ hint }}</span>
  </label>
</template>
