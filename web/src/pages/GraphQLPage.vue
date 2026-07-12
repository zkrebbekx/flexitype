<script setup lang="ts">
import { ref } from 'vue'
import { useMutation } from '@tanstack/vue-query'
import { api, friendlyError, type GraphQLResponse } from '@/lib/api'
import PageHeader from '@/components/ui/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import { Play } from 'lucide-vue-next'

const DEFAULT_QUERY = `# Read-only GraphQL over your live schema.
# Discover types, then query one with an optional FQL filter.
{
  _schemaTypes
}`

const query = ref(DEFAULT_QUERY)
const result = ref<GraphQLResponse | null>(null)
const error = ref('')

const run = useMutation({
  mutationFn: () => api.graphql(query.value),
  onSuccess: (res) => {
    error.value = ''
    result.value = res
  },
  onError: (e) => {
    error.value = friendlyError(e)
  },
})

function pretty(v: unknown): string {
  return JSON.stringify(v, null, 2)
}
</script>

<template>
  <PageHeader title="GraphQL">
    A read-only GraphQL API generated from your live type definitions: entity fields are attributes,
    relationships are nested selections. Pass an FQL string as a <span class="mono">filter</span> argument.
    <template #actions>
      <Button variant="primary" :disabled="run.isPending.value" @click="run.mutate()">
        <Play :size="15" /> Run
      </Button>
    </template>
  </PageHeader>

  <div class="grid gap-4 lg:grid-cols-2">
    <div class="flex flex-col">
      <label class="mb-1 text-[13px] font-medium text-(--text-muted)">Query</label>
      <textarea
        v-model="query"
        spellcheck="false"
        class="mono min-h-[320px] w-full resize-y rounded-lg border border-(--border) bg-(--surface) p-3 text-[13px] leading-relaxed outline-none focus:border-(--accent)"
        @keydown.ctrl.enter="run.mutate()"
        @keydown.meta.enter="run.mutate()"
      />
      <p class="mt-1 text-[12px] text-(--text-muted)">⌘/Ctrl + Enter to run.</p>
    </div>

    <div class="flex flex-col">
      <label class="mb-1 text-[13px] font-medium text-(--text-muted)">Result</label>
      <div class="min-h-[320px] overflow-auto rounded-lg border border-(--border) bg-(--canvas) p-3">
        <p v-if="error" class="rounded-md bg-(--danger-soft) px-3 py-2 text-[13px] text-(--danger)">{{ error }}</p>
        <template v-else-if="result">
          <pre v-if="result.errors" class="mono mb-2 whitespace-pre-wrap text-[13px] text-(--danger)">{{ pretty(result.errors) }}</pre>
          <pre v-if="result.data" class="mono whitespace-pre-wrap text-[13px] text-(--text-secondary)">{{ pretty(result.data) }}</pre>
        </template>
        <p v-else class="text-[13px] text-(--text-muted)">Run a query to see results.</p>
      </div>
    </div>
  </div>
</template>
