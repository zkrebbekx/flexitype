<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { api, friendlyError } from '@/lib/api'
import type { WebhookSubscription } from '@/lib/api'
import { useToasts } from '@/composables/useToasts'
import Drawer from '@/components/ui/Drawer.vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Toggle from '@/components/ui/Toggle.vue'

const props = defineProps<{ open: boolean; subscription?: WebhookSubscription }>()
const emit = defineEmits<{ close: [] }>()

const toasts = useToasts()
const queryClient = useQueryClient()

const form = reactive({
  name: '',
  url: '',
  secret: '',
  event_types: '',
  active: true,
})
const error = ref('')
const isEdit = ref(false)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    error.value = ''
    const s = props.subscription
    isEdit.value = !!s
    form.name = s?.name ?? ''
    form.url = s?.url ?? ''
    form.secret = ''
    form.event_types = (s?.event_types ?? []).join(', ')
    form.active = s?.active ?? true
  },
)

function eventTypes(): string[] {
  return form.event_types
    .split(',')
    .map((t) => t.trim())
    .filter(Boolean)
}

const save = useMutation({
  mutationFn: () => {
    if (isEdit.value && props.subscription) {
      return api.updateSubscription(props.subscription.id, {
        url: form.url,
        event_types: eventTypes(),
        active: form.active,
        rotate_secret: form.secret || undefined,
      })
    }
    return api.createSubscription({
      name: form.name,
      url: form.url,
      secret: form.secret || undefined,
      event_types: eventTypes(),
      active: form.active,
    })
  },
  onSuccess: (s) => {
    queryClient.invalidateQueries({ queryKey: ['webhook-subscriptions'] })
    toasts.success(isEdit.value ? `Subscription "${s.name}" updated` : `Subscription "${s.name}" created`)
    emit('close')
  },
  onError: (e) => (error.value = friendlyError(e)),
})
</script>

<template>
  <Drawer
    :open="open"
    :title="isEdit ? 'Edit subscription' : 'New webhook subscription'"
    subtitle="Deliver matching events as signed POSTs. Must be a public https endpoint; retries with backoff and dead-letters after ~3 days."
    @close="emit('close')"
  >
    <form class="flex flex-col gap-4" @submit.prevent="save.mutate()">
      <Input
        v-model="form.name"
        label="Name"
        mono
        placeholder="billing"
        :disabled="isEdit"
        hint="Unique per tenant; immutable"
      />
      <Input v-model="form.url" label="URL" placeholder="https://billing.internal/hooks/flexitype" />
      <Input
        v-model="form.secret"
        label="Signing secret"
        type="password"
        :placeholder="isEdit ? 'Leave blank to keep; set to rotate' : 'Optional HMAC secret'"
        :hint="isEdit ? 'Setting a new secret rotates it; the previous one stays valid for a grace window.' : 'Signs deliveries with HMAC-SHA256.'"
      />
      <Input
        v-model="form.event_types"
        label="Event types"
        placeholder="flexitype.attribute_value.set, flexitype.attribute_value.updated"
        hint="Comma-separated; leave blank for all events."
      />
      <Toggle v-model="form.active" label="Active" hint="Inactive subscriptions receive no deliveries." />

      <p v-if="error" class="rounded-md bg-(--danger-soft) px-3 py-2 text-[13px] text-(--danger)">{{ error }}</p>
    </form>

    <template #footer>
      <div class="flex justify-end gap-2">
        <Button @click="emit('close')">Cancel</Button>
        <Button variant="primary" :disabled="save.isPending.value" @click="save.mutate()">
          {{ isEdit ? 'Save' : 'Create subscription' }}
        </Button>
      </div>
    </template>
  </Drawer>
</template>
