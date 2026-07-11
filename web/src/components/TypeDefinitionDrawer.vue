<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { api, friendlyError } from '@/lib/api'
import type { TypeDefinition } from '@/lib/api'
import { useToasts } from '@/composables/useToasts'
import Drawer from '@/components/ui/Drawer.vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'

// Rename only: internal name, extends and kind are immutable after creation.
const props = defineProps<{ open: boolean; type?: TypeDefinition }>()
const emit = defineEmits<{ close: [] }>()

const toasts = useToasts()
const queryClient = useQueryClient()

const form = reactive({ display_name: '', description: '' })
const error = ref('')
const canSubmit = computed(() => !!form.display_name.trim())

watch(
  () => [props.open, props.type?.id],
  ([open]) => {
    if (!open) return
    error.value = ''
    form.display_name = props.type?.display_name ?? ''
    form.description = props.type?.description ?? ''
  },
  { immediate: true },
)

const save = useMutation({
  mutationFn: () => {
    if (!props.type) throw new Error('no type')
    return api.updateType(props.type.id, {
      display_name: form.display_name,
      description: form.description || undefined,
    })
  },
  onSuccess: (t) => {
    queryClient.invalidateQueries({ queryKey: ['type', props.type?.id] })
    queryClient.invalidateQueries({ queryKey: ['types'] })
    toasts.success(`"${t.display_name}" saved`)
    emit('close')
  },
  onError: (e) => (error.value = friendlyError(e)),
})
</script>

<template>
  <Drawer :open="open" title="Edit type" :subtitle="type?.internal_name" @close="emit('close')">
    <form class="flex flex-col gap-4" @submit.prevent="save.mutate()">
      <Input :model-value="type?.internal_name ?? ''" label="Internal name" mono disabled hint="Immutable" />
      <Input v-model="form.display_name" label="Display name" placeholder="Product" />
      <Input v-model="form.description" label="Description" placeholder="Optional" />
      <p v-if="error" class="rounded-md bg-(--danger-soft) px-3 py-2 text-[13px] text-(--danger)">{{ error }}</p>
    </form>
    <template #footer>
      <div class="flex justify-end gap-2">
        <Button @click="emit('close')">Cancel</Button>
        <Button variant="primary" :disabled="save.isPending.value || !canSubmit" @click="save.mutate()">Save changes</Button>
      </div>
    </template>
  </Drawer>
</template>
