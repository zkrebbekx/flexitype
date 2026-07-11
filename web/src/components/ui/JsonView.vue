<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ value: unknown }>()
const pretty = computed(() => JSON.stringify(props.value, null, 2))
</script>

<template>
  <pre class="mono max-h-72 overflow-auto rounded-md border border-(--border) bg-(--canvas) p-3 text-[12.5px] leading-relaxed whitespace-pre-wrap">{{ pretty }}</pre>
</template>
