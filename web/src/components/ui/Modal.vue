<script setup lang="ts">
defineProps<{ open: boolean; title: string; message: string; confirmLabel?: string; danger?: boolean }>()
const emit = defineEmits<{ close: []; confirm: [] }>()
</script>

<template>
  <Teleport to="body">
    <Transition name="fade">
      <div v-if="open" class="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-4" @click.self="emit('close')">
        <div role="alertdialog" aria-modal="true" class="w-full max-w-sm rounded-lg border border-(--border) bg-(--surface) p-5 shadow-xl">
          <h2 class="text-base font-semibold">{{ title }}</h2>
          <p class="mt-2 text-sm text-(--text-secondary)">{{ message }}</p>
          <div class="mt-5 flex justify-end gap-2">
            <slot name="actions">
              <button
                class="h-8.5 rounded-md border border-(--border-strong) px-3.5 text-sm font-medium"
                @click="emit('close')"
              >
                Cancel
              </button>
              <button
                class="h-8.5 rounded-md px-3.5 text-sm font-medium text-white"
                :class="danger ? 'bg-(--danger) hover:bg-(--danger-hover)' : 'bg-(--accent) hover:bg-(--accent-hover)'"
                @click="emit('confirm')"
              >
                {{ confirmLabel ?? 'Confirm' }}
              </button>
            </slot>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
