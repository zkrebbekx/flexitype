<script setup lang="ts">
defineProps<{ label: string; hint?: string; disabled?: boolean }>()
const model = defineModel<boolean>({ default: false })
</script>

<template>
  <label class="flex cursor-pointer items-start gap-2.5" :class="{ 'opacity-50': disabled }">
    <button
      type="button"
      role="switch"
      :aria-checked="model"
      :disabled="disabled"
      class="mt-0.5 h-5 w-9 shrink-0 rounded-full transition-colors"
      :class="model ? 'bg-(--accent)' : 'bg-(--border-strong)'"
      @click="model = !model"
    >
      <span
        class="block h-4 w-4 rounded-full bg-white shadow transition-transform"
        :class="model ? 'translate-x-4.5' : 'translate-x-0.5'"
      />
    </button>
    <span>
      <span class="block text-sm font-medium">{{ label }}</span>
      <span v-if="hint" class="block text-[13px] text-(--text-muted)">{{ hint }}</span>
    </span>
  </label>
</template>
