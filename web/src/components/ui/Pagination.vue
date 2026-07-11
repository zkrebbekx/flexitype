<script setup lang="ts">
import type { PageInfo } from '@/lib/api'

defineProps<{ pageInfo?: PageInfo; loading?: boolean }>()
const emit = defineEmits<{ next: [cursor: string]; reset: [] }>()
</script>

<template>
  <div v-if="pageInfo" class="flex items-center justify-between py-3 text-[13px] text-(--text-muted)">
    <span class="tnum">{{ pageInfo.total_count }} total</span>
    <div class="flex gap-2">
      <button
        v-if="pageInfo.has_previous_page"
        class="rounded-md border border-(--border-strong) px-2.5 py-1 font-medium text-(--text-secondary) hover:border-(--text-muted)"
        @click="emit('reset')"
      >
        First page
      </button>
      <button
        v-if="pageInfo.has_next_page && pageInfo.next_cursor"
        :disabled="loading"
        class="rounded-md border border-(--border-strong) px-2.5 py-1 font-medium text-(--text-secondary) hover:border-(--text-muted) disabled:opacity-50"
        @click="emit('next', pageInfo.next_cursor!)"
      >
        Next page
      </button>
    </div>
  </div>
</template>
