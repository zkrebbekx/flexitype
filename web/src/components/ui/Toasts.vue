<script setup lang="ts">
import { useToasts } from '@/composables/useToasts'
import { CircleCheck, CircleX, X } from 'lucide-vue-next'

const { toasts, dismiss } = useToasts()
</script>

<template>
  <Teleport to="body">
    <div class="pointer-events-none fixed right-4 bottom-4 z-[60] flex w-80 flex-col gap-2" aria-live="polite">
      <TransitionGroup name="fade">
        <div
          v-for="t in toasts"
          :key="t.id"
          class="pointer-events-auto flex items-start gap-2 rounded-lg border border-(--border) bg-(--surface-raised) p-3 text-sm shadow-lg"
        >
          <CircleCheck v-if="t.kind === 'success'" :size="17" class="mt-px shrink-0 text-(--ok)" />
          <CircleX v-else :size="17" class="mt-px shrink-0 text-(--danger)" />
          <p class="flex-1">{{ t.message }}</p>
          <button class="text-(--text-muted) hover:text-(--text)" aria-label="Dismiss" @click="dismiss(t.id)">
            <X :size="15" />
          </button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>
