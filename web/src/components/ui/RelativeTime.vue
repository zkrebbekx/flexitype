<script setup lang="ts">
import { formatRelative, formatTimestamp } from '@/lib/format'

// One relative timestamp with the absolute time always available on hover
// (title) and to assistive tech (datetime). Keeps every screen's
// relative-time rendering consistent.
defineProps<{ iso: string }>()
</script>

<template>
  <time :datetime="iso" :title="formatTimestamp(iso)">{{ formatRelative(iso) }}</time>
</template>
