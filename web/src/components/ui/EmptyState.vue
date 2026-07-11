<script setup lang="ts">
defineProps<{ title: string; body?: string }>()
</script>

<template>
  <div class="flex flex-col items-center gap-2 rounded-lg border border-dashed border-(--border-strong) px-6 py-12 text-center">
    <p class="text-sm font-medium text-(--text-secondary)">{{ title }}</p>
    <p v-if="body" class="max-w-sm text-[13px] text-(--text-muted)">{{ body }}</p>
    <div v-if="$slots.action" class="mt-2">
      <slot name="action" />
    </div>
  </div>
</template>
