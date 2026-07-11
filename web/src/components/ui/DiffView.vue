<script setup lang="ts">
import { computed } from 'vue'

// Flat top-level diff of the activity log's before/after descriptors.
// Nested values render as JSON; changed keys are highlighted, unchanged
// keys collapsed behind a toggle.
import { ref } from 'vue'

const props = defineProps<{
  before?: Record<string, unknown>
  after?: Record<string, unknown>
}>()

const showUnchanged = ref(false)

interface Row {
  key: string
  before?: string
  after?: string
  changed: boolean
}

const rows = computed<Row[]>(() => {
  const keys = new Set([...Object.keys(props.before ?? {}), ...Object.keys(props.after ?? {})])
  const out: Row[] = []
  for (const key of [...keys].sort()) {
    const b = props.before ? JSON.stringify(props.before[key]) : undefined
    const a = props.after ? JSON.stringify(props.after[key]) : undefined
    out.push({ key, before: b, after: a, changed: b !== a })
  }
  return out
})

const changedCount = computed(() => rows.value.filter((r) => r.changed).length)
</script>

<template>
  <div class="overflow-hidden rounded-md border border-(--border) text-[12.5px]">
    <table class="w-full">
      <thead>
        <tr class="border-b border-(--border) bg-(--canvas) text-left text-(--text-muted)">
          <th class="w-40 px-3 py-1.5 font-medium">Field</th>
          <th class="px-3 py-1.5 font-medium">Before</th>
          <th class="px-3 py-1.5 font-medium">After</th>
        </tr>
      </thead>
      <tbody>
        <template v-for="r in rows" :key="r.key">
          <tr v-if="r.changed || showUnchanged" class="border-b border-(--border) last:border-0 align-top">
            <td class="mono px-3 py-1.5 text-(--text-secondary)">{{ r.key }}</td>
            <td class="mono px-3 py-1.5 break-all" :class="r.changed ? 'bg-(--danger-soft) text-(--danger)' : 'text-(--text-muted)'">
              {{ r.before ?? '—' }}
            </td>
            <td class="mono px-3 py-1.5 break-all" :class="r.changed ? 'bg-(--ok-soft) text-(--ok)' : 'text-(--text-muted)'">
              {{ r.after ?? '—' }}
            </td>
          </tr>
        </template>
      </tbody>
    </table>
    <button
      v-if="rows.length > changedCount"
      class="w-full border-t border-(--border) bg-(--canvas) py-1.5 text-[12px] text-(--text-muted) hover:text-(--text-secondary)"
      @click="showUnchanged = !showUnchanged"
    >
      {{ showUnchanged ? 'Hide' : 'Show' }} {{ rows.length - changedCount }} unchanged fields
    </button>
  </div>
</template>
