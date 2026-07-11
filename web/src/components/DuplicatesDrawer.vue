<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { api, friendlyError } from '@/lib/api'
import type { MatchCandidate, MatchStrategy } from '@/lib/api'
import { useToasts } from '@/composables/useToasts'
import Drawer from '@/components/ui/Drawer.vue'
import Button from '@/components/ui/Button.vue'
import Select from '@/components/ui/Select.vue'
import Input from '@/components/ui/Input.vue'
import { Trash2 } from 'lucide-vue-next'

const props = defineProps<{
  open: boolean
  typeId: string
  typeName: string
  attributes: { id: string; internal_name: string; display_name: string }[]
}>()
const emit = defineEmits<{ close: [] }>()

const toasts = useToasts()
const queryClient = useQueryClient()

const rules = useQuery({
  queryKey: ['match-rules', () => props.typeId],
  queryFn: () => api.listMatchRules(props.typeId),
  enabled: computed(() => props.open && !!props.typeId),
})

const form = reactive({
  attribute_definition_id: '',
  strategy: 'trigram' as MatchStrategy,
  threshold: '0.7',
})
const error = ref('')
const activeRuleId = ref('')
const candidates = ref<MatchCandidate[]>([])
const scanned = ref(false)

watch(
  () => props.open,
  (open) => {
    if (!open) {
      activeRuleId.value = ''
      candidates.value = []
      scanned.value = false
      error.value = ''
    }
  },
)

const attrOptions = computed(() => [
  { value: '', label: 'Select attribute…' },
  ...props.attributes.map((a) => ({ value: a.id, label: `${a.display_name} (${a.internal_name})` })),
])
const strategyOptions = [
  { value: 'trigram', label: 'Trigram similarity' },
  { value: 'exact', label: 'Exact match' },
  { value: 'case_insensitive', label: 'Case-insensitive match' },
]
const attrName = (id: string) => props.attributes.find((a) => a.id === id)?.display_name ?? id

const createRule = useMutation({
  mutationFn: () =>
    api.createMatchRule(props.typeId, {
      attribute_definition_id: form.attribute_definition_id,
      strategy: form.strategy,
      threshold: form.strategy === 'trigram' ? Number(form.threshold) : undefined,
    }),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['match-rules'] })
    form.attribute_definition_id = ''
    toasts.success('Matching rule added')
  },
  onError: (e) => (error.value = friendlyError(e)),
})

const deleteRule = useMutation({
  mutationFn: (id: string) => api.deleteMatchRule(id),
  onSuccess: (_r, id) => {
    queryClient.invalidateQueries({ queryKey: ['match-rules'] })
    if (activeRuleId.value === id) {
      activeRuleId.value = ''
      candidates.value = []
      scanned.value = false
    }
  },
  onError: (e) => toasts.error(friendlyError(e)),
})

const scan = useMutation({
  mutationFn: (id: string) => api.scanMatchRule(id),
  onSuccess: (res, id) => {
    activeRuleId.value = id
    candidates.value = res.candidates
    scanned.value = true
  },
  onError: (e) => toasts.error(friendlyError(e)),
})

const dismiss = useMutation({
  mutationFn: (c: MatchCandidate) => api.dismissMatch(activeRuleId.value, c.entity_a, c.entity_b),
  onSuccess: (_r, c) => {
    candidates.value = candidates.value.filter((x) => !(x.entity_a === c.entity_a && x.entity_b === c.entity_b))
    toasts.success('Pair dismissed')
  },
  onError: (e) => toasts.error(friendlyError(e)),
})

const canAdd = computed(() => !!form.attribute_definition_id && !createRule.isPending.value)
</script>

<template>
  <Drawer
    :open="open"
    :title="`Duplicates — ${typeName}`"
    subtitle="Define matching rules, then scan for probable duplicate entities. Report-only."
    @close="emit('close')"
  >
    <div class="flex flex-col gap-4">
      <fieldset class="flex flex-col gap-3 rounded-md border border-(--border) p-3">
        <legend class="px-1 text-[13px] font-medium text-(--text-secondary)">Add a rule</legend>
        <Select v-model="form.attribute_definition_id" label="Attribute" :options="attrOptions" />
        <div class="grid grid-cols-2 gap-3">
          <Select v-model="form.strategy" label="Strategy" :options="strategyOptions" />
          <Input v-if="form.strategy === 'trigram'" v-model="form.threshold" type="number" label="Threshold (0–1)" />
        </div>
        <p v-if="error" class="rounded-md bg-(--danger-soft) px-3 py-2 text-[13px] text-(--danger)">{{ error }}</p>
        <div class="flex justify-end">
          <Button variant="primary" size="sm" :disabled="!canAdd" @click="createRule.mutate()">Add rule</Button>
        </div>
      </fieldset>

      <div>
        <p class="mb-1.5 text-[13px] font-medium text-(--text-secondary)">Rules</p>
        <p v-if="!rules.data.value?.items.length" class="text-[13px] text-(--text-muted)">No rules yet.</p>
        <div
          v-for="rule in rules.data.value?.items ?? []"
          :key="rule.id"
          class="mb-1.5 flex items-center justify-between gap-3 rounded-md border border-(--border) bg-(--surface) px-3 py-2 text-sm"
        >
          <span class="min-w-0 truncate">
            <span class="font-medium">{{ attrName(rule.attribute_definition_id) }}</span>
            <span class="ml-1.5 text-[12px] text-(--text-muted)">
              {{ rule.strategy }}<template v-if="rule.strategy === 'trigram'"> ≥ {{ rule.threshold }}</template>
            </span>
          </span>
          <span class="flex items-center gap-1.5">
            <Button size="sm" :disabled="scan.isPending.value" @click="scan.mutate(rule.id)">Scan</Button>
            <Button size="sm" variant="ghost" aria-label="Delete rule" @click="deleteRule.mutate(rule.id)">
              <Trash2 :size="14" />
            </Button>
          </span>
        </div>
      </div>

      <div v-if="scanned">
        <p class="mb-1.5 text-[13px] font-medium text-(--text-secondary)">
          Candidates <span class="text-(--text-muted)">({{ candidates.length }})</span>
        </p>
        <p v-if="!candidates.length" class="text-[13px] text-(--text-muted)">No probable duplicates found.</p>
        <div class="max-h-80 overflow-y-auto rounded-md border border-(--border)">
          <div
            v-for="c in candidates"
            :key="`${c.entity_a}|${c.entity_b}`"
            class="flex items-center justify-between gap-3 border-b border-(--border) px-3 py-2 text-[13px] last:border-b-0"
          >
            <span class="min-w-0">
              <span class="mono">{{ c.entity_a }}</span> ↔ <span class="mono">{{ c.entity_b }}</span>
              <span class="ml-2 rounded bg-(--accent-soft) px-1.5 py-0.5 text-[11px] text-(--accent)">
                {{ Math.round(c.score * 100) }}%
              </span>
              <span class="block truncate text-[12px] text-(--text-muted)">“{{ c.value_a }}” · “{{ c.value_b }}”</span>
            </span>
            <Button size="sm" variant="ghost" :disabled="dismiss.isPending.value" @click="dismiss.mutate(c)">Dismiss</Button>
          </div>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <Button @click="emit('close')">Close</Button>
      </div>
    </template>
  </Drawer>
</template>
