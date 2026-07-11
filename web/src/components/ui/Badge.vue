<script setup lang="ts">
withDefaults(defineProps<{ tone?: 'neutral' | 'accent' | 'ok' | 'warn' | 'danger' }>(), {
  tone: 'neutral',
})
</script>

<template>
  <span
    class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[12px] font-medium"
    :class="{
      'bg-(--border) text-(--text-secondary)': tone === 'neutral',
      'bg-(--accent-soft) text-(--accent)': tone === 'accent',
      'bg-(--ok-soft) text-(--ok)': tone === 'ok',
      'bg-(--warn-soft) text-(--warn)': tone === 'warn',
      'bg-(--danger-soft) text-(--danger)': tone === 'danger',
    }"
  >
    <slot />
  </span>
</template>
