<script setup lang="ts">
defineProps<{ tabs: { key: string; label: string; count?: number }[] }>()
const model = defineModel<string>({ required: true })
</script>

<template>
  <div role="tablist" class="flex gap-1 border-b border-(--border)">
    <button
      v-for="t in tabs"
      :key="t.key"
      role="tab"
      :aria-selected="model === t.key"
      class="-mb-px flex items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium transition-colors"
      :class="
        model === t.key
          ? 'border-(--accent) text-(--text)'
          : 'border-transparent text-(--text-muted) hover:text-(--text-secondary)'
      "
      @click="model = t.key"
    >
      {{ t.label }}
      <span v-if="t.count !== undefined" class="tnum rounded-full bg-(--border) px-1.5 text-[11px]">{{
        t.count
      }}</span>
    </button>
  </div>
</template>
