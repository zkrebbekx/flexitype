<script setup lang="ts">
import type { PageInfo } from '@/lib/api'

// canPrevious comes from the paging history (a client-side cursor stack),
// since opaque cursors can't be walked backward from the current page.
defineProps<{ pageInfo?: PageInfo; loading?: boolean; canPrevious?: boolean }>()
const emit = defineEmits<{ next: [cursor: string]; previous: []; reset: [] }>()
</script>

<template>
  <div v-if="pageInfo" class="flex items-center justify-between py-3 text-[13px] text-(--text-muted)">
    <span v-if="pageInfo.total_count != null" class="tnum">{{ pageInfo.total_count }} total</span>
    <span v-else></span>
    <div class="flex gap-2">
      <button
        v-if="canPrevious"
        class="rounded-md border border-(--border-strong) px-2.5 py-1 font-medium text-(--text-secondary) hover:border-(--text-muted)"
        @click="emit('reset')"
      >
        First
      </button>
      <button
        v-if="canPrevious"
        :disabled="loading"
        class="rounded-md border border-(--border-strong) px-2.5 py-1 font-medium text-(--text-secondary) hover:border-(--text-muted) disabled:opacity-50"
        @click="emit('previous')"
      >
        Previous
      </button>
      <button
        v-if="pageInfo.has_next_page && pageInfo.next_cursor"
        :disabled="loading"
        class="rounded-md border border-(--border-strong) px-2.5 py-1 font-medium text-(--text-secondary) hover:border-(--text-muted) disabled:opacity-50"
        @click="emit('next', pageInfo.next_cursor!)"
      >
        Next
      </button>
    </div>
  </div>
</template>
