<script setup lang="ts">
// Shown when a data load fails. Without it, screens fall through to their
// empty state on error, so "request failed" looks identical to "no data
// yet". Drop it in as a sibling and guard the empty state on !isError.
import { AlertCircle, RefreshCw } from 'lucide-vue-next'
import Button from './Button.vue'
import { friendlyError } from '@/lib/api'

defineProps<{ error: unknown; title?: string }>()
const emit = defineEmits<{ retry: [] }>()
</script>

<template>
  <div class="flex flex-col items-center gap-3 rounded-lg border border-(--danger) bg-(--danger-soft) px-6 py-10 text-center">
    <AlertCircle :size="22" class="text-(--danger)" />
    <div>
      <p class="text-sm font-medium text-(--danger)">{{ title ?? "Couldn't load this" }}</p>
      <p class="mt-1 max-w-md text-[13px] text-(--text-secondary)">{{ friendlyError(error) }}</p>
    </div>
    <Button size="sm" @click="emit('retry')"><RefreshCw :size="14" /> Retry</Button>
  </div>
</template>
