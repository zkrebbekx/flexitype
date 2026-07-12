<script setup lang="ts">
import { useId } from 'vue'

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

// Stable ids so assistive tech can announce the error/hint via the input.
const errorId = useId()
const hintId = useId()
</script>

<template>
  <label class="block">
    <span v-if="label" class="mb-1 block text-[13px] font-medium text-(--text-secondary)">{{ label }}</span>
    <input
      v-model="model"
      :type="type ?? 'text'"
      :placeholder="placeholder"
      :disabled="disabled"
      :aria-invalid="error ? 'true' : undefined"
      :aria-describedby="error ? errorId : hint ? hintId : undefined"
      class="h-8.5 w-full rounded-md border bg-(--surface) px-2.5 text-sm text-(--text) placeholder:text-(--text-muted) disabled:opacity-50"
      :class="[error ? 'border-(--danger)' : 'border-(--border-strong)', mono ? 'mono' : '']"
    />
    <span v-if="error" :id="errorId" role="alert" class="mt-1 block text-[13px] text-(--danger)">{{ error }}</span>
    <span v-else-if="hint" :id="hintId" class="mt-1 block text-[13px] text-(--text-muted)">{{ hint }}</span>
  </label>
</template>
