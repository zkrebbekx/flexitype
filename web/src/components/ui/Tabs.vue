<script setup lang="ts">
import { ref, useId } from 'vue'

const props = defineProps<{ tabs: { key: string; label: string; count?: number }[] }>()
const model = defineModel<string>({ required: true })

// Per-instance ids so each tab can label its panel and point aria-controls
// at it. Parents render the panels and bind these via the exposed helpers.
const base = useId()
const tabId = (key: string) => `${base}-tab-${key}`
const panelId = (key: string) => `${base}-panel-${key}`

const listRef = ref<HTMLElement>()

// Roving-tabindex keyboard nav: Left/Right cycle, Home/End jump to ends,
// and focus follows selection (the automatic-activation tab pattern).
function onKeydown(e: KeyboardEvent) {
  const keys = props.tabs.map((t) => t.key)
  if (keys.length === 0) return
  const current = keys.indexOf(model.value)
  let next = -1
  if (e.key === 'ArrowRight') next = (current + 1) % keys.length
  else if (e.key === 'ArrowLeft') next = (current - 1 + keys.length) % keys.length
  else if (e.key === 'Home') next = 0
  else if (e.key === 'End') next = keys.length - 1
  else return
  e.preventDefault()
  model.value = keys[next]
  listRef.value?.querySelectorAll<HTMLElement>('[role="tab"]')[next]?.focus()
}

defineExpose({ tabId, panelId })
</script>

<template>
  <div ref="listRef" role="tablist" class="flex gap-1 border-b border-(--border)" @keydown="onKeydown">
    <button
      v-for="t in tabs"
      :id="tabId(t.key)"
      :key="t.key"
      role="tab"
      :aria-selected="model === t.key"
      :aria-controls="panelId(t.key)"
      :tabindex="model === t.key ? 0 : -1"
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
