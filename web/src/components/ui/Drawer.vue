<script setup lang="ts">
import { ref, toRef, useId } from 'vue'
import { X } from 'lucide-vue-next'
import { useFocusTrap } from '@/composables/useFocusTrap'

const props = defineProps<{ open: boolean; title: string; subtitle?: string }>()
const emit = defineEmits<{ close: [] }>()

const panel = ref<HTMLElement>()
const titleId = useId()
useFocusTrap(toRef(props, 'open'), panel, () => emit('close'))
</script>

<template>
  <Teleport to="body">
    <Transition name="fade">
      <div v-if="open" class="fixed inset-0 z-40 bg-black/30" @click="emit('close')" />
    </Transition>
    <Transition name="slide">
      <aside
        v-if="open"
        ref="panel"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="titleId"
        class="fixed inset-y-0 right-0 z-50 flex w-full max-w-xl flex-col border-l border-(--border) bg-(--surface) shadow-xl"
      >
        <header class="flex items-start justify-between gap-4 border-b border-(--border) px-5 py-4">
          <div>
            <h2 :id="titleId" class="text-base font-semibold">{{ title }}</h2>
            <p v-if="subtitle" class="mt-0.5 text-[13px] text-(--text-muted)">{{ subtitle }}</p>
          </div>
          <button
            class="rounded-md p-1 text-(--text-muted) hover:bg-(--canvas) hover:text-(--text)"
            aria-label="Close"
            @click="emit('close')"
          >
            <X :size="18" />
          </button>
        </header>
        <div class="flex-1 overflow-y-auto px-5 py-4">
          <slot />
        </div>
        <footer v-if="$slots.footer" class="border-t border-(--border) px-5 py-3">
          <slot name="footer" />
        </footer>
      </aside>
    </Transition>
  </Teleport>
</template>
