<script setup lang="ts">
import type { DataType } from '@/lib/api'

const props = defineProps<{ dataType: DataType }>()

// Glyphs make attribute tables scannable without reading labels.
const glyphs: Record<DataType, string> = {
  string: 'Aa',
  integer: '#',
  float: '1.0',
  decimal: '1.00',
  bool: '◧',
  enum: '⋯',
  date: '⏱',
  time: '⏱',
  datetime: '⏱',
  url: '⌘',
  email: '@',
  json: '{}',
}

const glyph = glyphs[props.dataType] ?? '?'
</script>

<template>
  <span
    class="inline-flex items-center gap-1.5 rounded-md border border-(--border) bg-(--canvas) px-1.5 py-0.5 text-[12px] text-(--text-secondary)"
  >
    <span class="mono font-semibold text-(--accent)">{{ glyph }}</span>
    {{ dataType }}
  </span>
</template>
