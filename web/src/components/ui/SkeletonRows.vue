<script setup lang="ts">
withDefaults(defineProps<{ rows?: number; cols?: number }>(), { rows: 5, cols: 4 })
</script>

<template>
  <tr v-for="r in rows" :key="r" class="border-b border-(--border) last:border-0">
    <td v-for="c in cols" :key="c" class="px-3 py-2.5">
      <div class="h-3.5 animate-pulse rounded bg-(--border)" :style="{ width: `${45 + ((r * c * 37) % 40)}%` }" />
    </td>
  </tr>
</template>
